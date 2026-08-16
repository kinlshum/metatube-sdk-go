#!/usr/bin/env python3

import html
import json
import os
import re
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from html.parser import HTMLParser

FLARE_URL = os.getenv("FLARE_URL", "http://192.168.10.170:8191/v1")
MDC_URL = os.getenv("MDC_URL", "http://192.168.10.170:9208")
HOST_QUERY_ROOT = os.getenv("HOST_QUERY_ROOT", "/host-downloads/.metatube-provider")
MDC_QUERY_ROOT = os.getenv("MDC_QUERY_ROOT", "/downloads_kraken/.metatube-provider")


def request_json(url, method="GET", payload=None, timeout=180):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            body = response.read()
            return json.loads(body) if body.strip() else {}
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")
        raise RuntimeError(f"{method} {url}: HTTP {exc.code}: {detail}") from exc


def fc2_number(value):
    match = re.search(r"(?i)(?:FC2[\s_-]*(?:PPV[\s_-]*)?)?(\d{5,})", value)
    return match.group(1) if match else ""


def jav_code(value):
    patterns = (
        r"(?i)(?<![A-Z0-9])([A-Z]{1,10}\d{0,4})[-_ ](\d{2,6})(?!\d)",
        r"(?i)(?<![A-Z0-9])(\d{2,6}[A-Z]{2,10})[-_ ](\d{2,6})(?!\d)",
    )
    for pattern in patterns:
        match = re.search(pattern, value)
        if match:
            return f"{match.group(1)}-{match.group(2)}".upper()
    return ""


def strip_markup(value):
    value = re.sub(r"<[^>]+>", " ", value)
    return html.unescape(" ".join(value.split()))


def element_body(document, element_id):
    match = re.search(
        rf'<(div|td|span)[^>]+id=["\']{re.escape(element_id)}["\'][^>]*>(.*?)</\1>',
        document,
        re.I | re.S,
    )
    return match.group(2) if match else ""


def linked_text(document, element_id, href_fragment):
    body = element_body(document, element_id)
    return [strip_markup(value) for value in re.findall(
        rf'<a[^>]+href=["\'][^"\']*{re.escape(href_fragment)}[^"\']*["\'][^>]*>(.*?)</a>',
        body,
        re.I | re.S,
    ) if strip_markup(value)]


def flare_get(url):
    result = request_json(FLARE_URL, "POST", {
        "cmd": "request.get", "url": url, "maxTimeout": 60000,
    }, timeout=75)
    solution = result.get("solution", {})
    if result.get("status") != "ok" or solution.get("status") != 200:
        raise LookupError(f"FlareSolverr request failed: {url}")
    return solution.get("url", url), solution.get("response", "")


def xslist_search(keyword):
    _, document = flare_get(
        "https://xslist.org/search?query=" + urllib.parse.quote(keyword) + "&lg=zh"
    )
    results = []
    for card in re.findall(r'<li[^>]*>(.*?)</li>', document, re.I | re.S):
        link = re.search(
            r'<h3>\s*<a[^>]+title=["\']([^"\']+)["\'][^>]+href=["\']([^"\']+/model/(\d+)\.html)',
            card, re.I | re.S,
        )
        if not link:
            continue
        names = [value.strip() for value in html.unescape(link.group(1)).split(" - ") if value.strip()]
        image = re.search(r'<img[^>]+src=["\']([^"\']+)', card, re.I)
        results.append({
            "id": link.group(3), "name": names[-1] if names else keyword,
            "provider": "XsList", "homepage": html.unescape(link.group(2)),
            "aliases": names[:-1],
            "images": [html.unescape(image.group(1))]
            if image and "anonymous" not in image.group(1) else [],
        })
    if not results:
        raise LookupError("XsList returned no matching actor")
    return results


def xslist_actor(actor_id):
    homepage, document = flare_get(f"https://xslist.org/zh/model/{actor_id}.html")
    name_match = re.search(
        r'<span[^>]+itemprop=["\']name["\'][^>]*>(.*?)</span>',
        document, re.I | re.S,
    )
    if not name_match:
        raise LookupError("XsList returned no actor name")
    name = strip_markup(name_match.group(1))
    heading = re.search(r'<h1[^>]*>(.*?)</h1>', document, re.I | re.S)
    aliases = []
    if heading:
        aliases = [value.strip() for value in re.findall(
            r'\(([^)]+)\)', strip_markup(heading.group(1))
        ) if value.strip()]
    gallery = element_body(document, "gallery")
    images = list(dict.fromkeys(html.unescape(value) for value in re.findall(
        r'<(?:a|img)[^>]+(?:href|src)=["\']([^"\']+)["\'][^>]*>',
        gallery, re.I,
    ) if "anonymous" not in value and re.search(
        r'\.(?:jpe?g|png|webp)(?:\?|$)', value, re.I
    )))
    profile = re.search(
        r'<meta[^>]+(?:name|property)=["\'](?:image|og:image)["\'][^>]+'
        r'content=["\']([^"\']+)', document, re.I,
    )
    if profile and "anonymous" not in profile.group(1):
        images.insert(0, html.unescape(profile.group(1)))
    images = list(dict.fromkeys(images))
    details_match = re.search(
        r'<h2[^>]*>.*?个人资料.*?</h2>\s*<p[^>]*>(.*?)</p>',
        document, re.I | re.S,
    )
    details = strip_markup(details_match.group(1)) if details_match else ""

    def field(label):
        match = re.search(
            rf'{label}\s*:\s*(.*?)(?=\s+(?:出生|三围|罩杯|出道日期|星座|血型|身高|国籍)\s*:|$)',
            details,
        )
        return match.group(1).strip() if match else ""

    height_match = re.search(r'身高\s*:\s*(\d+)', details)
    birthday_match = re.search(r'(\d{4})年(\d{1,2})月(\d{1,2})日', field("出生"))
    birthday = (f"{birthday_match.group(1)}-{int(birthday_match.group(2)):02d}-"
                f"{int(birthday_match.group(3)):02d}T00:00:00Z") if birthday_match else None
    return {
        "id": actor_id, "name": name, "provider": "XsList", "homepage": homepage,
        "summary": "", "hobby": "", "skill": "", "blood_type": field("血型"),
        "cup_size": field("罩杯").replace("Cup", "").strip(),
        "measurements": field("三围").replace(" ", ""),
        "nationality": field("国籍"),
        "height": int(height_match.group(1)) if height_match else 0,
        "aliases": aliases, "images": images, "birthday": birthday,
    }


def javlibrary(code):
    search_url = (
        "https://www.javlibrary.com/ja/vl_searchbyid.php?keyword="
        + urllib.parse.quote(code)
    )
    result = request_json(FLARE_URL, "POST", {
        "cmd": "request.get", "url": search_url, "maxTimeout": 60000,
    }, timeout=75)
    solution = result.get("solution", {})
    if result.get("status") != "ok" or solution.get("status") != 200:
        raise LookupError("JavLibrary search request failed")
    homepage = solution.get("url", search_url)
    document = solution.get("response", "")

    if 'id="video_title"' not in document:
        candidates = re.findall(
            r'<a[^>]+href=["\']([^"\']+\.html)["\'][^>]*>(.*?)</a>',
            document,
            re.I | re.S,
        )
        match = next(((href, title) for href, title in candidates
                      if code.casefold() in strip_markup(title).casefold()), None)
        if not match:
            raise LookupError("JavLibrary returned no matching movie")
        homepage = urllib.parse.urljoin(homepage, html.unescape(match[0]))
        result = request_json(FLARE_URL, "POST", {
            "cmd": "request.get", "url": homepage, "maxTimeout": 60000,
        }, timeout=75)
        solution = result.get("solution", {})
        if result.get("status") != "ok" or solution.get("status") != 200:
            raise LookupError("JavLibrary movie request failed")
        homepage = solution.get("url", homepage)
        document = solution.get("response", "")

    def field(element_id):
        body = element_body(document, element_id)
        cells = re.findall(
            r'<(?:td|span)[^>]*class=["\']text["\'][^>]*>(.*?)</(?:td|span)>',
            body,
            re.I | re.S,
        )
        return strip_markup(cells[-1]) if cells else ""

    title_body = element_body(document, "video_title")
    title_match = re.search(r"<a[^>]*>(.*?)</a>", title_body, re.I | re.S)
    title = strip_markup(title_match.group(1)) if title_match else strip_markup(title_body)
    title = re.sub(rf"(?i)^\s*{re.escape(code)}\s*", "", title).strip()
    number = field("video_id") or code
    if not title:
        raise LookupError("JavLibrary returned no title")
    cover_match = re.search(
        r'<img[^>]+id=["\']video_jacket_img["\'][^>]+src=["\']([^"\']+)',
        document,
        re.I,
    )
    cover = html.unescape(cover_match.group(1)) if cover_match else ""
    if not cover:
        raise LookupError("JavLibrary returned no cover")
    date = field("video_date")
    runtime_match = re.search(r"\d+", field("video_length"))
    score_match = re.search(r'<span[^>]+class=["\']score["\'][^>]*>\(([^)]+)\)', document, re.I)
    description_match = re.search(
        r'<meta[^>]+name=["\']Description["\'][^>]+content=["\']([^"\']*)',
        document,
        re.I,
    )
    return {
        "id": number, "number": number, "title": title,
        "summary": html.unescape(description_match.group(1)) if description_match else "",
        "provider": "JavLibrary", "homepage": homepage,
        "director": (linked_text(document, "video_director", "vl_director.php") or [""])[0],
        "actors": linked_text(document, "video_cast", "vl_star.php"),
        "thumb_url": cover, "big_thumb_url": cover,
        "cover_url": cover, "big_cover_url": cover,
        "preview_video_url": "", "preview_video_hls_url": "",
        "preview_images": [],
        "maker": (linked_text(document, "video_maker", "vl_maker.php") or [""])[0],
        "label": (linked_text(document, "video_label", "vl_label.php") or [""])[0],
        "series": "", "genres": linked_text(document, "video_genres", "vl_genre.php"),
        "score": float(score_match.group(1)) if score_match else 0,
        "runtime": int(runtime_match.group()) if runtime_match else 0,
        "release_date": f"{date}T00:00:00Z" if date else None,
    }


def javdb(code):
    search_url = (
        "https://javdb.com/search?f=all&locale=en&q="
        + urllib.parse.quote(code)
    )
    _, document = flare_get(search_url)
    cards = re.findall(
        r'<a\s+href=["\'](/v/[^"\']+)["\'][^>]*class=["\'][^"\']*box[^"\']*["\']'
        r'[^>]*>.*?<div[^>]+class=["\']video-title["\'][^>]*>\s*'
        r'<strong>([^<]+)</strong>',
        document,
        re.I | re.S,
    )
    match = next(((href, number) for href, number in cards
                  if strip_markup(number).casefold() == code.casefold()), None)
    if not match:
        raise LookupError("JavDB returned no matching movie")

    homepage, document = flare_get(
        urllib.parse.urljoin("https://javdb.com", match[0]) + "?locale=en"
    )

    title_match = re.search(
        r'<strong[^>]+class=["\']current-title["\'][^>]*>(.*?)</strong>',
        document,
        re.I | re.S,
    )
    title = strip_markup(title_match.group(1)) if title_match else ""
    if not title:
        raise LookupError("JavDB returned no title")

    cover_match = re.search(
        r'<img[^>]+src=["\']([^"\']+)["\'][^>]+class=["\'][^"\']*video-cover',
        document,
        re.I,
    )
    cover = html.unescape(cover_match.group(1)) if cover_match else ""
    if not cover:
        raise LookupError("JavDB returned no cover")

    def panel(label):
        match = re.search(
            rf'<div[^>]+class=["\'][^"\']*panel-block[^"\']*["\'][^>]*>\s*'
            rf'<strong>{re.escape(label)}:</strong>.*?'
            r'<span[^>]+class=["\']value["\'][^>]*>(.*?)</span>',
            document,
            re.I | re.S,
        )
        return match.group(1) if match else ""

    def panel_text(label):
        return strip_markup(panel(label))

    def panel_links(label, fragment):
        return [strip_markup(value) for value in re.findall(
            rf'<a[^>]+href=["\'][^"\']*{re.escape(fragment)}[^"\']*["\'][^>]*>(.*?)</a>',
            panel(label),
            re.I | re.S,
        ) if strip_markup(value)]

    runtime_match = re.search(r"\d+", panel_text("Duration"))
    rating_match = re.search(r"([0-9]+(?:\.[0-9]+)?)", panel_text("Rating"))
    date = panel_text("Released Date")
    previews = list(dict.fromkeys(html.unescape(value) for value in re.findall(
        r'<a[^>]+data-fancybox=["\']gallery["\'][^>]+href=["\']([^"\']+)',
        document,
        re.I,
    ) if html.unescape(value) != cover))
    return {
        "id": code, "number": code, "title": title, "summary": "",
        "provider": "JavDB", "homepage": homepage,
        "director": (panel_links("Director", "/directors/") or [""])[0],
        "actors": panel_links("Actor(s)", "/actors/"),
        "thumb_url": cover, "big_thumb_url": cover,
        "cover_url": cover, "big_cover_url": cover,
        "preview_video_url": "", "preview_video_hls_url": "",
        "preview_images": previews,
        "maker": (panel_links("Maker", "/makers/") or [""])[0],
        "label": (panel_links("Publisher", "/publishers/") or [""])[0],
        "series": (panel_links("Series", "/series/") or [""])[0],
        "genres": panel_links("Tags", "/tags"),
        "score": float(rating_match.group(1)) if rating_match else 0,
        "runtime": int(runtime_match.group()) if runtime_match else 0,
        "release_date": f"{date}T00:00:00Z" if date else None,
    }


class FC2Parser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.title_depth = 0
        self.title = []
        self.row = None
        self.cell = None
        self.cells = []
        self.rows = []
        self.images = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "h1" and not self.title:
            self.title_depth = 1
        elif self.title_depth:
            self.title_depth += 1
        if tag == "tr":
            self.row, self.cells = [], []
        elif self.row is not None and tag in ("th", "td"):
            self.cell = []
        elif self.cell is not None and tag in ("a", "span", "br"):
            self.cell.append(" ")
        if tag == "img" and attrs.get("src"):
            self.images.append(attrs["src"])

    def handle_endtag(self, tag):
        if self.title_depth:
            self.title_depth -= 1
        if self.row is not None and tag in ("th", "td") and self.cell is not None:
            self.cells.append(" ".join("".join(self.cell).split()))
            self.cell = None
        elif tag == "tr" and self.row is not None:
            if len(self.cells) >= 2:
                self.rows.append((self.cells[0], self.cells[1]))
            self.row = None

    def handle_data(self, data):
        if self.title_depth:
            self.title.append(data)
        if self.cell is not None:
            self.cell.append(data)


def fc2cmadb(number):
    homepage = f"https://fc2cmadb.com/articles/{number}"
    solution = request_json(FLARE_URL, "POST", {
        "cmd": "request.get", "url": homepage, "maxTimeout": 60000
    })
    if solution.get("status") != "ok" or solution.get("solution", {}).get("status") != 200:
        raise LookupError("FC2CMADB request failed")
    parser = FC2Parser()
    parser.feed(solution["solution"]["response"])
    fields = {re.sub(r"[：:]$", "", key.strip()): value for key, value in parser.rows}
    get = lambda *keys: next((fields[key] for key in keys if fields.get(key)), "")
    title = html.unescape(" ".join("".join(parser.title).split()))
    if not title:
        raise LookupError("FC2CMADB returned no title")
    images = [urllib.parse.urljoin(homepage, image) for image in parser.images]
    cover = images[0] if images else "https://fc2cmadb.com/favicon.ico"
    tags = get("タグ", "Tag", "Tags").split()
    actors = [v.strip() for v in re.split(r"[,、/]", get("女優", "Actress")) if v.strip()]
    date = get("販売日", "Sale date")
    runtime_text = get("収録時間", "Recording time")
    runtime = 0
    parts = runtime_text.split(":")
    if len(parts) == 3 and all(part.isdigit() for part in parts):
        runtime = int(parts[0]) * 60 + int(parts[1])
    return {
        "id": number, "number": f"FC2-{number}", "title": title,
        "summary": "", "provider": "FC2CMADB", "homepage": homepage,
        "director": "", "actors": actors, "thumb_url": cover,
        "big_thumb_url": cover, "cover_url": cover, "big_cover_url": cover,
        "preview_video_url": "", "preview_video_hls_url": "",
        "preview_images": images[1:], "maker": get("販売者", "Seller"),
        "label": "", "series": "", "genres": tags, "score": 0,
        "runtime": runtime,
        "release_date": f"{date}T00:00:00Z" if date else None,
    }


class JSONLDParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.capture = False
        self.parts = []
        self.documents = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "script" and attrs.get("type", "").lower() == "application/ld+json":
            self.capture = True
            self.parts = []

    def handle_endtag(self, tag):
        if tag == "script" and self.capture:
            self.capture = False
            try:
                value = json.loads("".join(self.parts))
                self.documents.extend(value if isinstance(value, list) else [value])
            except (TypeError, ValueError):
                pass

    def handle_data(self, data):
        if self.capture:
            self.parts.append(data)


def javten(number):
    search_url = f"https://javten.com/search?kw={urllib.parse.quote(number)}"
    result = request_json(FLARE_URL, "POST", {
        "cmd": "request.get", "url": search_url, "maxTimeout": 60000
    }, timeout=75)
    solution = result.get("solution", {})
    if result.get("status") != "ok" or solution.get("status") != 200:
        raise LookupError("JavTen search request failed")
    homepage = solution.get("url", "")
    response = solution.get("response", "")
    if not re.search(r"/video/\d+/id\d+", homepage):
        matches = re.findall(r'href=["\']([^"\']*/video/\d+/id\d+[^"\']*)', response, re.I)
        if not matches:
            raise LookupError("JavTen returned no matching movie")
        homepage = urllib.parse.urljoin("https://javten.com/", html.unescape(matches[0]))
        result = request_json(FLARE_URL, "POST", {
            "cmd": "request.get", "url": homepage, "maxTimeout": 60000
        }, timeout=75)
        solution = result.get("solution", {})
        if result.get("status") != "ok" or solution.get("status") != 200:
            raise LookupError("JavTen movie request failed")
        homepage = solution.get("url", homepage)
        response = solution.get("response", "")

    parser = JSONLDParser()
    parser.feed(response)
    movie = next((value for value in parser.documents
                  if isinstance(value, dict) and value.get("@type") == "Movie"), None)
    if not movie:
        raise LookupError("JavTen returned no movie metadata")

    def names(value):
        if not isinstance(value, list):
            value = [value] if value else []
        output = []
        for item in value:
            name = item.get("name", "") if isinstance(item, dict) else str(item)
            if name.strip():
                output.append(name.strip())
        return output

    title = html.unescape(str(movie.get("name", "")).strip())
    if not title:
        raise LookupError("JavTen returned no title")
    cover = movie.get("image", "")
    if isinstance(cover, list):
        cover = cover[0] if cover else ""
    elif isinstance(cover, dict):
        cover = cover.get("url", "")
    genres = names(movie.get("genre", []))
    actors = names(movie.get("actor", []))
    runtime = 0
    duration = str(movie.get("duration", ""))
    match = re.fullmatch(r"PT(?:(\d+)H)?(?:(\d+)M)?", duration)
    if match:
        runtime = int(match.group(1) or 0) * 60 + int(match.group(2) or 0)
    date = str(movie.get("datePublished", "")).strip().replace("/", "-")
    director = movie.get("director", "")
    if isinstance(director, dict):
        director = director.get("name", "")
    return {
        "id": number, "number": f"FC2-{number}", "title": title,
        "summary": html.unescape(str(movie.get("description", "")).strip()),
        "provider": "fc2hub", "homepage": homepage, "director": str(director),
        "actors": actors, "thumb_url": cover, "big_thumb_url": cover,
        "cover_url": cover, "big_cover_url": cover, "preview_video_url": "",
        "preview_video_hls_url": "", "preview_images": [], "maker": str(director),
        "label": "", "series": "", "genres": genres, "score": 0,
        "runtime": runtime, "release_date": f"{date}T00:00:00Z" if date else None,
    }


def mdcng(number):
    os.makedirs(HOST_QUERY_ROOT, exist_ok=True)
    query_dir = tempfile.mkdtemp(prefix=f"fc2-{number}-", dir=HOST_QUERY_ROOT)
    os.chmod(query_dir, 0o777)
    name = os.path.basename(query_dir)
    host_video = os.path.join(query_dir, f"FC2-PPV-{number}.mp4")
    mdc_dir = f"{MDC_QUERY_ROOT}/{name}"
    mdc_video = f"{mdc_dir}/FC2-PPV-{number}.mp4"
    try:
        subprocess.run([
            "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i",
            "color=c=black:s=16x16:d=1", "-an", "-c:v", "mpeg4", "-y",
            host_video,
        ], check=True)
        os.truncate(host_video, 101 * 1024 * 1024)
        os.chmod(host_video, 0o666)
        marker = f"metatube-{number}-{int(time.time())}"
        request_json(f"{MDC_URL}/api/manual-jobs", "POST", {
            "pathes": [mdc_video], "target_folder": mdc_dir, "link_mode": 3,
            "delete_empty_parent_after_move": False, "ts_id": marker,
            "copy_config_from": None,
        })
        job_id = None
        deadline = time.time() + 180
        while time.time() < deadline and job_id is None:
            jobs = request_json(f"{MDC_URL}/api/manual-jobs?page=1&page_size=50")
            for job in jobs.get("data", []):
                if mdc_video in job.get("source_pathes", ""):
                    job_id = job["id"]
                    break
            time.sleep(2)
        if job_id is None:
            raise LookupError("MDC-NG job was not created")
        while time.time() < deadline:
            job = request_json(f"{MDC_URL}/api/manual-jobs/{job_id}")
            if job.get("status") == 2:
                break
            time.sleep(2)
        if job.get("status") != 2 or job.get("error_count", 0):
            raise LookupError("MDC-NG returned no metadata")
        nfos = [os.path.join(query_dir, value) for value in os.listdir(query_dir)
                if value.lower().endswith(".nfo")]
        if not nfos:
            raise LookupError("MDC-NG returned no NFO")
        root = ET.parse(nfos[0]).getroot()
        one = lambda *names: next((root.findtext(name, "").strip() for name in names
                                   if root.findtext(name, "").strip()), "")
        many = lambda name: list(dict.fromkeys(
            node.text.strip() for node in root.findall(name) if node.text and node.text.strip()))
        cover = one("cover")
        if not cover:
            raise LookupError("MDC-NG metadata has no remote cover URL")
        date = one("premiered", "releasedate", "release")
        runtime_text = one("runtime")
        return {
            "id": number, "number": f"FC2-{number}",
            "title": one("title", "originaltitle"), "summary": one("plot", "outline"),
            "provider": "MDC-NG", "homepage": one("website") or f"http://192.168.10.170:9208",
            "director": one("director"), "actors": many("actor/name"),
            "thumb_url": cover, "big_thumb_url": cover, "cover_url": cover,
            "big_cover_url": cover, "preview_video_url": "",
            "preview_video_hls_url": "", "preview_images": [],
            "maker": one("maker", "studio"), "label": one("label"),
            "series": one("series"), "genres": many("genre"), "score": 0,
            "runtime": int(runtime_text) if runtime_text.isdigit() else 0,
            "release_date": f"{date}T00:00:00Z" if date else None,
        }
    finally:
        shutil.rmtree(query_dir, ignore_errors=True)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        actor_search = re.fullmatch(r"/v1/providers/XsList/actors\?q=(.*)", self.path)
        actor_info = re.fullmatch(r"/v1/providers/XsList/actors/(\d+)", self.path)
        if actor_search or actor_info:
            try:
                result = (xslist_search(urllib.parse.unquote_plus(actor_search.group(1)))
                          if actor_search else xslist_actor(actor_info.group(1)))
                body = json.dumps(result, ensure_ascii=False).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except Exception as exc:
                body = json.dumps({"error": str(exc)}, ensure_ascii=False).encode()
                self.send_response(404)
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            return
        match = re.fullmatch(r"/v1/providers/(FC2CMADB|MDC-NG|fc2hub|JavLibrary|JavDB)/movies/(.+)", self.path)
        if not match:
            self.send_error(404)
            return
        provider_name = match.group(1)
        number = jav_code(urllib.parse.unquote(match.group(2))) if provider_name in ("JavLibrary", "JavDB") else fc2_number(match.group(2))
        if not number:
            self.send_error(400, "invalid movie number")
            return
        try:
            if provider_name == "JavLibrary":
                result = javlibrary(number)
            elif provider_name == "JavDB":
                result = javdb(number)
            elif provider_name == "FC2CMADB":
                result = fc2cmadb(number)
            elif provider_name == "MDC-NG":
                result = mdcng(number)
            else:
                result = javten(number)
            body = json.dumps(result, ensure_ascii=False).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as exc:
            body = json.dumps({"error": str(exc)}, ensure_ascii=False).encode()
            self.send_response(404)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    def log_message(self, fmt, *args):
        print(fmt % args, flush=True)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 9210), Handler).serve_forever()
