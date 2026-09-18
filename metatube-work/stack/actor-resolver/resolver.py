#!/usr/bin/env python3

import json
import os
import re
import threading
import time
import urllib.parse
import urllib.request
from html import unescape
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

METATUBE_URL = os.getenv("METATUBE_URL", "http://metatube:8080").rstrip("/")
PROVIDER_BRIDGE_URL = os.getenv("PROVIDER_BRIDGE_URL", "http://provider-bridge:9210").rstrip("/")
CACHE_FILE = os.getenv("CACHE_FILE", "/data/cache.json")
OVERRIDES_FILE = os.getenv("OVERRIDES_FILE", "/data/overrides.json")
FLARE_URL = os.getenv("FLARE_URL", "http://flaresolverr:8191/v1")
CACHE_TTL = int(os.getenv("CACHE_TTL", "604800"))
SOURCE_ORDER = tuple(value.strip() for value in os.getenv(
    "SOURCE_ORDER", "AV-LEAGUE,XsList,JavLibrary,Minnano-AV,JAVDatabase,Babepedia,Gfriends"
).split(",") if value.strip())
TIMEOUT = int(os.getenv("HTTP_TIMEOUT", "120"))
_lock = threading.Lock()


def request_json(url):
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
        return json.load(response)


def request_text(url):
    request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
        return response.read().decode("utf-8", errors="replace")


def flare_text(url):
    payload = json.dumps({"cmd": "request.get", "url": url, "maxTimeout": TIMEOUT * 1000}).encode()
    request = urllib.request.Request(FLARE_URL, data=payload, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=TIMEOUT + 10) as response:
        result = json.load(response)
    if result.get("status") != "ok":
        raise RuntimeError(result.get("message", "FlareSolverr request failed"))
    return result.get("solution", {}).get("response", "")


def clean_html(value):
    return re.sub(r"\s+", " ", unescape(re.sub(r"<[^>]+>", " ", value or ""))).strip()


def identity_key(value):
    return re.sub(r"[^a-z0-9\u3040-\u30ff\u3400-\u9fff]", "", value.casefold())


def same_identity(left, right):
    if identity_key(left) == identity_key(right):
        return True
    if japanese(left) or japanese(right):
        return False
    return sorted(re.findall(r"[a-z0-9]+", left.casefold())) == sorted(re.findall(r"[a-z0-9]+", right.casefold()))


def enrichment_identities(values):
    identities = list(dict.fromkeys(value for value in values if value))
    for value in tuple(identities):
        words = value.split()
        if len(words) == 2 and not japanese(value):
            reversed_name = " ".join(reversed(words))
            if reversed_name not in identities:
                identities.append(reversed_name)
    return identities


def profile_value(document, label):
    pattern = rf"<span[^>]*>\s*{re.escape(label)}\s*:?</span>\s*<span[^>]*>(.*?)</span>"
    match = re.search(pattern, document, re.IGNORECASE | re.DOTALL)
    return clean_html(match.group(1)) if match else ""


def minnano_value(document, label):
    pattern = rf"<span[^>]*>\s*{re.escape(label)}\s*</span>\s*<p[^>]*>(.*?)</p>"
    match = re.search(pattern, document, re.IGNORECASE | re.DOTALL)
    return clean_html(match.group(1)) if match else ""


def minnano_detail(actor_id):
    homepage = f"https://www.minnano-av.com/actress{actor_id}.html"
    document = request_text(homepage)
    found = re.search(r'<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>',
                      document, re.IGNORECASE | re.DOTALL)
    schema = json.loads(unescape(found.group(1))) if found else {}
    aliases = [schema.get("alternateName", ""), schema.get("additionalName", "")]
    former = minnano_value(document, "別名")
    former_match = re.match(r"(.+?)(?:【[^】]+】)?\s*（(.+?)）", former)
    if former_match:
        aliases.append(former_match.group(1).strip())
        aliases.extend(value.strip() for value in former_match.group(2).split("/") if value.strip())
    elif former:
        aliases.append(former)
    size = minnano_value(document, "サイズ")
    height_match = re.search(r"T(\d+)", size)
    cup_match = re.search(r"([A-Z])カップ", size)
    debut = minnano_value(document, "デビュー作品")
    debut_match = re.search(r"（(\d{4})年(\d{2})月\s*(\d{2})日）", debut)
    sign_match = re.search(r"）\s*([^\s<]+座)", minnano_value(document, "生年月日"))
    return {
        "id": str(actor_id), "name": schema.get("name", ""), "provider": "Minnano-AV",
        "homepage": homepage, "aliases": list(dict.fromkeys(value for value in aliases if value)),
        "images": [schema["image"]] if schema.get("image") else [],
        "birthday": schema.get("birthDate", ""),
        "debut_date": "-".join(debut_match.groups()) if debut_match else "",
        "debut_title": re.sub(r"（\d{4}年\d{2}月\s*\d{2}日）\s*$", "", debut).strip(),
        "av_appearance_period": minnano_value(document, "AV出演期間"), "tags": [],
        "blood_type": "", "cup_size": cup_match.group(1) if cup_match else "",
        "measurements": "/".join(re.findall(r"[BWH]\d+", size)), "nationality": "日本",
        "height": int(height_match.group(1)) if height_match else 0,
        "hobby": minnano_value(document, "趣味・特技"), "skill": "",
        "summary": "", "sign": sign_match.group(1) if sign_match else "",
    }


def minnano_search_detail(name):
    url = "https://www.minnano-av.com/search_result.php?" + urllib.parse.urlencode({
        "search_scope": "actress", "search_word": name, "search": "Go"
    })
    document = request_text(url)
    candidates = re.findall(r'(?:https://www\.minnano-av\.com/)?actress(\d+)\.html', document)
    for actor_id in dict.fromkeys(candidates):
        detail = minnano_detail(actor_id)
        if exact_match(detail, enrichment_identities([name])):
            return detail
    return None


def xslist_search_detail(name):
    url = "https://xslist.org/search?" + urllib.parse.urlencode({"query": name, "lg": "en"})
    document = flare_text(url)
    anchors = re.findall(r'<a\b([^>]*)>(.*?)</a>', document, re.IGNORECASE | re.DOTALL)
    for attributes, body in anchors:
        href = re.search(r'href="(?:https://xslist\.org)?/en/model/(\d+)\.html"', attributes, re.I)
        if not href:
            continue
        actor_id = href.group(1)
        title_match = re.search(r'title="([^"]*)"', attributes, re.I)
        title = title_match.group(1) if title_match else ""
        label = clean_html(title or body)
        parts = [value.strip() for value in re.split(r"\s+-\s+", label) if value.strip()]
        original = next((value for value in parts if japanese(value)), "")
        western = next((value for value in parts if not japanese(value)), "")
        if not original or identity_key(original) != identity_key(name):
            continue
        image = re.search(rf'https://xslist\.org/kojav/model2/\d+/{re.escape(actor_id)}\.jpg', document)
        context = clean_html(body)
        birthday_match = re.search(r"(?:Born|Birthday)\D+(\d{1,2})/(\d{1,2})/(\d{4})", context, re.I)
        birthday = ""
        if birthday_match:
            month, day, year = birthday_match.groups()
            birthday = f"{year}-{int(month):02d}-{int(day):02d}"
        return {
            "id": actor_id, "name": original, "provider": "XsList",
            "homepage": f"https://xslist.org/en/model/{actor_id}.html",
            "aliases": [western] if western else [],
            "images": [image.group(0)] if image else [], "birthday": birthday,
            "debut_date": "", "debut_title": "", "av_appearance_period": "",
            "tags": [], "blood_type": "", "cup_size": "", "measurements": "",
            "nationality": "日本", "height": 0, "hobby": "", "skill": "",
            "summary": "", "sign": "",
        }
    return None


def javlibrary_detail(name):
    search_url = "https://www.javlibrary.com/ja/vl_searchbykeyword.php?" + urllib.parse.urlencode({
        "keyword": name
    })
    document = flare_text(search_url)
    stars = re.findall(
        r'href="(?:https://www\.javlibrary\.com/ja/)?vl_star\.php\?s=([a-z0-9]+)"[^>]*>(.*?)</a>',
        document, re.IGNORECASE | re.DOTALL,
    )
    for actor_id, label in stars:
        if identity_key(clean_html(label)) != identity_key(name):
            continue
        homepage = f"https://www.javlibrary.com/en/vl_star.php?s={actor_id}"
        english = flare_text(homepage)
        heading = re.search(r"Videos\s+starring\s+([^<\r\n]+)", english, re.IGNORECASE)
        western = clean_html(heading.group(1)) if heading else ""
        return {
            "id": actor_id, "name": name, "provider": "JavLibrary",
            "homepage": homepage, "aliases": [western] if western else [], "images": [],
            "birthday": "", "debut_date": "", "debut_title": "",
            "av_appearance_period": "", "tags": [], "blood_type": "",
            "cup_size": "", "measurements": "", "nationality": "日本",
            "height": 0, "hobby": "", "skill": "", "summary": "", "sign": "",
        }
    return None


def babepedia_detail(names):
    for name in names:
        if not name or japanese(name):
            continue
        search_url = "https://www.babepedia.com/ajax-search.php?" + urllib.parse.urlencode({"term": name})
        try:
            response = flare_text(search_url)
            pre = re.search(r"<pre[^>]*>(.*?)</pre>", response, re.IGNORECASE | re.DOTALL)
            results = json.loads(unescape(pre.group(1)) if pre else response)
        except (json.JSONDecodeError, OSError, RuntimeError):
            continue
        match = next((item for item in results if same_identity(item.get("label", ""), name)), None)
        if not match:
            continue
        actor_name = match["label"].strip()
        slug = match.get("value", actor_name).replace(" ", "_")
        homepage = f"https://www.babepedia.com/babe/{urllib.parse.quote(slug)}"
        document = flare_text(homepage)
        aliases_match = re.search(r'<h2[^>]*id="aka"[^>]*>(.*?)</h2>', document, re.IGNORECASE | re.DOTALL)
        aliases = [value.strip() for value in clean_html(aliases_match.group(1)).split(" - ") if value.strip()] if aliases_match else []
        born = profile_value(document, "Born")
        date_match = re.search(r"(\d{1,2})(?:st|nd|rd|th)? of ([A-Za-z]+) (\d{4})", born)
        birthday = ""
        if date_match:
            months = {month: index for index, month in enumerate(
                ("January", "February", "March", "April", "May", "June", "July", "August",
                 "September", "October", "November", "December"), 1)}
            birthday = f"{date_match.group(3)}-{months[date_match.group(2)]:02d}-{int(date_match.group(1)):02d}"
        images = re.findall(r'<div[^>]*id="profbox2".*?<a[^>]*class="img"[^>]*href="([^"]+)"', document,
                            re.IGNORECASE | re.DOTALL)
        bio = re.search(r'<p[^>]*id="biotext"[^>]*>(.*?)</p>', document, re.IGNORECASE | re.DOTALL)
        return {
            "id": slug, "name": actor_name, "provider": "Babepedia", "homepage": homepage,
            "aliases": aliases, "images": [urllib.parse.urljoin(homepage, value) for value in images],
            "birthday": birthday, "debut_date": "", "blood_type": "",
            "cup_size": profile_value(document, "Bra/cup size"),
            "measurements": profile_value(document, "Measurements").removesuffix(" ("),
            "nationality": profile_value(document, "Nationality"),
            "height": int(match.group(1)) if (match := re.search(r"(\d+) cm", profile_value(document, "Height"))) else 0,
            "hobby": "", "skill": "", "summary": clean_html(bio.group(1)) if bio else ""
        }
    return None


def javdatabase_detail(names):
    for name in names:
        if not name or japanese(name):
            continue
        url = "https://www.javdatabase.com/?" + urllib.parse.urlencode({"s": name, "post_type": "idols"})
        try:
            search = flare_text(url)
        except (OSError, RuntimeError):
            continue
        links = list(dict.fromkeys(re.findall(r'https://www\.javdatabase\.com/idols/[a-z0-9_-]+/?', search)))
        for homepage in links[:12]:
            try:
                document = flare_text(homepage)
            except (OSError, RuntimeError):
                continue
            heading = re.search(r'<h1[^>]*class="idol-name"[^>]*>(.*?)</h1>', document, re.IGNORECASE | re.DOTALL)
            actor_name = re.sub(r"\s*-\s*JAV\s*Profile\s*$", "", clean_html(heading.group(1))) if heading else ""
            if not same_identity(actor_name, name):
                continue
            def labeled(label):
                found = re.search(rf'<b[^>]*>\s*{re.escape(label)}\s*:?</b>(.*?)(?:<b|<br|</p>)', document,
                                  re.IGNORECASE | re.DOTALL)
                return clean_html(found.group(1)) if found else ""
            image = re.search(r'<div[^>]*class="idol-portrait".*?<img[^>]*(?:data-src|src)="([^"]+)"', document,
                              re.IGNORECASE | re.DOTALL)
            return {
                "id": homepage.rstrip("/").rsplit("/", 1)[-1], "name": actor_name,
                "provider": "JAVDatabase", "homepage": homepage, "aliases": [],
                "images": [urllib.parse.urljoin(homepage, image.group(1))] if image else [],
                "birthday": labeled("DOB"), "debut_date": "", "blood_type": "", "cup_size": "",
                "measurements": labeled("Measurements"), "nationality": "",
                "height": int(match.group(1)) if (match := re.search(r"(\d+)", labeled("Height"))) else 0,
                "hobby": "", "skill": "", "summary": ""
            }
    return None


def japanese(value):
    return any("\u3040" <= char <= "\u30ff" or "\u3400" <= char <= "\u9fff" for char in value)


def latin_alias(values):
    for value in values:
        if value and re.search(r"[A-Za-z]", value) and not japanese(value):
            return value.strip()
    return ""


def western_name(details, aliases):
    """Choose a Western name from sources whose Romanization order is known."""
    for provider in ("JavLibrary", "Minnano-AV"):
        for detail in details:
            if detail.get("provider") != provider:
                continue
            value = latin_alias([detail.get("name", ""), *detail.get("aliases", [])])
            words = value.split()
            if len(words) == 2:
                return " ".join(reversed(words))
            if value:
                return value
    return latin_alias(aliases)


def clean_aliases(values, canonical_name):
    primary_latin = canonical_name.split(" (", 1)[0].strip()
    cleaned = []
    for value in values:
        value = re.sub(r"^Also known as:\s*", "", value or "", flags=re.IGNORECASE).strip()
        if not value:
            continue
        if re.search(r"[A-Za-z]", value):
            value = primary_latin
        if value and value.casefold() not in {item.casefold() for item in cleaned}:
            cleaned.append(value)
    return cleaned


def lookup_name(value):
    value = re.sub(r"^\[JAV_CUSTOM_PROVIDER\]\s*", "", value.strip())
    canonical = re.search(r"\(JAP、(?:\d{4}|\?)、([^()]+)\)\s*$", value)
    if canonical:
        return canonical.group(1).strip()
    parenthetical = re.search(r"\(([^()]*)\)\s*$", value)
    if parenthetical and japanese(parenthetical.group(1)):
        return parenthetical.group(1).strip()
    return value


def mapped_canonical_name(value):
    value = re.sub(r"^\[JAV_CUSTOM_PROVIDER\]\s*", "", value.strip())
    return value if re.search(r"\([^()]*、[^()]*、[^()]+\)\s*$", value) else ""


def iso_date(value):
    match = re.match(r"(\d{4})-(\d{2})-(\d{2})", value or "")
    return match.group(0) if match and match.group(1) != "0001" else ""


def load_cache():
    try:
        with open(CACHE_FILE, encoding="utf-8") as handle:
            return json.load(handle)
    except (FileNotFoundError, json.JSONDecodeError):
        return {}


def save_cache(cache):
    os.makedirs(os.path.dirname(CACHE_FILE), exist_ok=True)
    temporary = CACHE_FILE + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        json.dump(cache, handle, ensure_ascii=False, indent=2, sort_keys=True)
    os.replace(temporary, CACHE_FILE)


def actor_override(name, aliases):
    try:
        with open(OVERRIDES_FILE, encoding="utf-8") as handle:
            entries = json.load(handle).get("actors", [])
    except (FileNotFoundError, json.JSONDecodeError):
        return None
    identities = {value.casefold() for value in [name, *aliases] if value}
    for entry in entries:
        mapped = [entry.get("original", ""), entry.get("canonical", ""), *entry.get("aliases", [])]
        if any(value and value.casefold() in identities for value in mapped):
            return entry
    return None


def exact_match(candidate, identities):
    names = [candidate.get("name", ""), *candidate.get("aliases", [])]
    folded = {value.casefold() for value in identities if value}
    return any(value and value.casefold() in folded for value in names)


def actor_detail(result):
    provider = urllib.parse.quote(result["provider"], safe="")
    actor_id = urllib.parse.quote(str(result["id"]), safe="")
    if result["provider"] == "Minnano-AV":
        return minnano_detail(actor_id)
    if result["provider"] == "XsList":
        return request_json(
            f"{PROVIDER_BRIDGE_URL}/v1/providers/{provider}/actors/{actor_id}"
        )
    return request_json(f"{METATUBE_URL}/v1/actors/{provider}/{actor_id}?lazy=false").get("data")


def first(details, field):
    for detail in details:
        value = detail.get(field)
        if value not in (None, "", 0, [], {}):
            return value, detail["provider"]
    return "", ""


def first_date(details, field):
    for detail in details:
        value = iso_date(detail.get(field))
        if value:
            return value, detail["provider"]
    return "", ""


def first_from(details, field, preferred_providers):
    ordered = sorted(
        details,
        key=lambda detail: preferred_providers.index(detail["provider"])
        if detail["provider"] in preferred_providers else len(preferred_providers),
    )
    return first_date(ordered, field) if field.endswith("_date") else first(ordered, field)


def place_of_birth(details):
    value, source = first(details, "place_of_birth")
    if value:
        return value, source
    for detail in details:
        summary = detail.get("summary", "")
        match = re.search(r"(?:出身地|Place of birth)[：:]\s*([^\n。,|]+)", summary, re.IGNORECASE)
        if match:
            return match.group(1).strip(), detail["provider"]
    return "", ""


def merge(name, details):
    details = sorted(details, key=lambda item: SOURCE_ORDER.index(item["provider"])
                     if item["provider"] in SOURCE_ORDER else len(SOURCE_ORDER))
    aliases = []
    images = []
    external_ids = {}
    urls = {}
    for detail in details:
        external_ids[detail["provider"]] = str(detail["id"])
        if detail.get("homepage"):
            urls[detail["provider"]] = detail["homepage"]
        for value in [detail.get("name", ""), *detail.get("aliases", [])]:
            if value and value.casefold() not in {item.casefold() for item in aliases}:
                aliases.append(value)
        for value in detail.get("images", []):
            if value and value not in images:
                images.append(value)

    english = western_name(details, aliases)
    original = name if japanese(name) else next((value for value in aliases if japanese(value)), name)
    birthday, birthday_source = first_date(details, "birthday")
    year = birthday[:4] if birthday else ""
    canonical_name = english or original
    if english and original:
        canonical_name = f"{english} (JAP、{year or '?'}、{original})"
    override = actor_override(name, aliases)
    if override:
        preferred = override.get("canonical", "").strip()
        original = override.get("original", original).strip()
        year = str(override.get("birth_year", year or "?")).strip()
        if preferred:
            canonical_name = f"{preferred} (JAP、{year or '?'}、{original})"
        for value in override.get("aliases", []):
            if value and value.casefold() not in {item.casefold() for item in aliases}:
                aliases.append(value)

    aliases = clean_aliases(aliases, canonical_name)
    if override:
        for value in override.get("historical_aliases", []):
            if value and value.casefold() not in {item.casefold() for item in aliases}:
                aliases.append(value)

    fields = {}
    provenance = {}
    for field in ("debut_date", "debut_title", "av_appearance_period", "blood_type",
                  "cup_size", "measurements", "nationality", "height", "hobby", "skill",
                  "summary", "sign"):
        if field in ("debut_date", "debut_title", "av_appearance_period"):
            value, source = first_from(details, field, ("Minnano-AV",))
        else:
            value, source = first(details, field)
        fields[field] = value
        if source:
            provenance[field] = source
    if birthday:
        provenance["birthday"] = birthday_source
    fields["place_of_birth"], source = place_of_birth(details)
    if source:
        provenance["place_of_birth"] = source
    if override:
        birthday = iso_date(override.get("birthday")) or birthday
        fields["debut_date"] = override.get("debut_date", fields["debut_date"])
        fields["debut_title"] = override.get("debut_title", fields["debut_title"])
        fields["av_appearance_period"] = override.get(
            "av_appearance_period", fields["av_appearance_period"]
        )
        if override.get("birthday"):
            provenance["birthday"] = "Minnano-AV"
        if override.get("debut_date"):
            provenance["debut_date"] = "Minnano-AV"
        external_ids.update(override.get("external_ids", {}))
        urls.update(override.get("urls", {}))

    conflicts = {}
    for field in ("birthday", "blood_type", "cup_size", "measurements", "height"):
        values = {}
        for detail in details:
            value = iso_date(detail.get(field)) if field == "birthday" else detail.get(field)
            if value not in (None, "", 0):
                values.setdefault(str(value), []).append(detail["provider"])
        if len(values) > 1:
            conflicts[field] = values

    stable_provider = next((provider for provider in ("AV-LEAGUE", "Gfriends", "XsList", "Minnano-AV")
                            if provider in external_ids), details[0]["provider"])
    return {
        "id": f"{stable_provider}:{external_ids[stable_provider]}",
        "name": canonical_name,
        "original_name": original,
        "provider": "JAVActorResolver",
        "homepage": urls.get(stable_provider, ""),
        "aliases": aliases,
        "images": images,
        "birthday": birthday,
        "debut_date": iso_date(fields["debut_date"]),
        "debut_title": fields["debut_title"],
        "av_appearance_period": fields["av_appearance_period"],
        "tags": list(dict.fromkeys(
            value for detail in details for value in detail.get("tags", []) if value
        )) if not override else list(dict.fromkeys([
            *(value for detail in details for value in detail.get("tags", []) if value),
            *override.get("tags", []),
        ])),
        "blood_type": fields["blood_type"],
        "cup_size": fields["cup_size"],
        "measurements": fields["measurements"],
        "nationality": fields["nationality"],
        "height": fields["height"],
        "hobby": fields["hobby"],
        "skill": fields["skill"],
        "summary": fields["summary"],
        "place_of_birth": fields["place_of_birth"],
        "sign": fields["sign"],
        "external_ids": external_ids,
        "urls": urls,
        "provenance": provenance,
        "conflicts": conflicts,
        "sources": [detail["provider"] for detail in details],
    }


def resolve(name, refresh=False):
    preferred_name = mapped_canonical_name(name)
    name = lookup_name(name)
    key = name.casefold()
    override = actor_override(name, [])
    search_name = override.get("original", "").strip() if override else name
    with _lock:
        cache = load_cache()
        cached = cache.get(key)
        if cached and not refresh and time.time() - cached["saved_at"] < CACHE_TTL:
            record = dict(cached["record"])
            if preferred_name:
                record["name"] = preferred_name
            return record

    search_url = f"{METATUBE_URL}/v1/actors/search?" + urllib.parse.urlencode({
        "q": search_name.strip(), "fallback": "true"
    })
    results = request_json(search_url).get("data", [])
    identities = [search_name.strip()]
    matched = [result for result in results if exact_match(result, identities)]
    details = []
    for result in matched:
        try:
            detail = actor_detail(result)
            if detail:
                details.append(detail)
        except Exception as error:
            print(f"actor source failed: {result.get('provider')}: {error}", flush=True)
    if not details:
        raise LookupError(f"no actor match for {name}")
    present = {detail["provider"] for detail in details}
    for provider, lookup in (
        ("XsList", xslist_search_detail),
        ("JavLibrary", javlibrary_detail),
        ("Minnano-AV", minnano_search_detail),
    ):
        if provider in present:
            continue
        try:
            detail = lookup(search_name.strip())
            if detail:
                details.append(detail)
                present.add(provider)
        except Exception as error:
            print(f"actor direct source failed: {provider}: {error}", flush=True)
    enrichment_names = []
    for detail in details:
        enrichment_names.extend([detail.get("name", ""), *detail.get("aliases", [])])
    for provider, lookup in (("JAVDatabase", javdatabase_detail), ("Babepedia", babepedia_detail)):
        try:
            detail = lookup(enrichment_identities(enrichment_names))
            if detail:
                details.append(detail)
                enrichment_names.extend([detail.get("name", ""), *detail.get("aliases", [])])
        except Exception as error:
            print(f"actor enrichment failed: {provider}: {error}", flush=True)
    record = merge(search_name.strip(), details)
    if preferred_name:
        record["name"] = preferred_name
        if preferred_name.casefold() not in {value.casefold() for value in record["aliases"]}:
            record["aliases"].append(preferred_name)
    with _lock:
        cache = load_cache()
        cache[key] = {"saved_at": time.time(), "record": record}
        save_cache(cache)
    return record


class Handler(BaseHTTPRequestHandler):
    def send_json(self, status, payload):
        body = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/health":
            self.send_json(200, {"status": "ok"})
            return
        if parsed.path != "/resolve":
            self.send_json(404, {"error": "not found"})
            return
        query = urllib.parse.parse_qs(parsed.query)
        name = query.get("name", [""])[0].strip()
        if not name:
            self.send_json(400, {"error": "name is required"})
            return
        try:
            self.send_json(200, {"data": resolve(name, query.get("refresh", [""])[0] == "true")})
        except Exception as error:
            self.send_json(404, {"error": str(error)})

    def log_message(self, message, *args):
        print(f"{self.client_address[0]} {message % args}", flush=True)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 9211), Handler).serve_forever()
