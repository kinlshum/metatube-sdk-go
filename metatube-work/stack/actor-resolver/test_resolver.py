import unittest
import json
import os
import tempfile

import resolver


class ResolverTests(unittest.TestCase):
    def test_parses_minnano_profile_and_former_name(self):
        document = '''
        <script type="application/ld+json">{"@type":"Person","name":"あかね麗",
        "alternateName":"あかねうらら","additionalName":"Akane Urara",
        "birthDate":"1999-11-30","image":"actor.jpg"}</script>
        <span>別名</span><p>二階堂麗【旧名】 （にかいどううらら / Nikaidou Urara）</p>
        <span>生年月日</span><p>1999年11月30日 （現在26歳）いて座</p>
        <span>サイズ</span><p>T162 / B88(<a>Fカップ</a>) / W56 / H88 / S</p>
        <span>趣味・特技</span><p>ヘアセット</p>
        <span>AV出演期間</span><p>2024年-</p>
        <span>デビュー作品</span><p>大型巨乳新人（2024年05月 21日）</p>
        '''
        original = resolver.request_text
        resolver.request_text = lambda _url: document
        try:
            detail = resolver.minnano_detail("251960")
        finally:
            resolver.request_text = original
        self.assertEqual("1999-11-30", detail["birthday"])
        self.assertEqual("2024-05-21", detail["debut_date"])
        self.assertEqual("Nikaidou Urara", detail["aliases"][-1])
        self.assertEqual("B88/W56/H88", detail["measurements"])
        self.assertEqual("いて座", detail["sign"])

    def test_invalid_date_falls_through_to_next_source(self):
        details = [
            {
                "id": "1", "name": "南見京", "provider": "AV-LEAGUE",
                "homepage": "", "aliases": ["Minami Kei"], "images": [],
                "birthday": "0001-01-01T00:00:00Z", "debut_date": "0001-01-01T00:00:00Z",
                "blood_type": "", "cup_size": "", "measurements": "",
                "nationality": "", "height": 0, "hobby": "", "skill": "", "summary": "",
            },
            {
                "id": "19739", "name": "南見京", "provider": "Minnano-AV",
                "homepage": "https://www.minnano-av.com/actress19739.html",
                "aliases": ["みなみけい", "Minami Kei"], "images": [],
                "birthday": "2005-01-01T00:00:00Z", "debut_date": "2026-01-16T00:00:00Z",
                "blood_type": "", "cup_size": "K", "measurements": "B112/W66/H96",
                "nationality": "日本", "height": 170, "hobby": "", "skill": "", "summary": "",
            },
        ]

        record = resolver.merge("南見京", details)

        self.assertEqual("2005-01-01", record["birthday"])
        self.assertEqual("Minnano-AV", record["provenance"]["birthday"])

    def test_minnano_debut_fields_flow_into_merged_record(self):
        details = [{
            "id": "38990", "name": "南見京", "provider": "AV-LEAGUE",
            "homepage": "https://www.av-league.com/actress/38990.html",
            "aliases": ["Minami Kei"], "images": [], "birthday": "",
            "debut_date": "2026-01-16T00:00:00Z", "debut_title": "",
            "av_appearance_period": "", "tags": [], "blood_type": "", "cup_size": "K",
            "measurements": "", "nationality": "", "height": 170, "hobby": "",
            "skill": "", "summary": "",
        }, {
            "id": "19739", "name": "南見京", "provider": "Minnano-AV",
            "homepage": "https://www.minnano-av.com/actress19739.html",
            "aliases": ["みなみけい", "Minami Kei"], "images": [],
            "birthday": "2005-01-01T00:00:00Z",
            "debut_date": "2026-01-20T00:00:00Z",
            "debut_title": "大型新人 南見京AVデビュー",
            "av_appearance_period": "2026年 -", "tags": ["長身", "巨乳"],
            "blood_type": "", "cup_size": "K", "measurements": "B112/W66/H96",
            "nationality": "日本", "height": 170, "hobby": "", "skill": "",
            "summary": "",
        }]

        record = resolver.merge("南見京", details)

        self.assertEqual("2026-01-20", record["debut_date"])
        self.assertEqual("大型新人 南見京AVデビュー", record["debut_title"])
        self.assertEqual("2026年 -", record["av_appearance_period"])
        self.assertEqual(["長身", "巨乳"], record["tags"])
        self.assertEqual("Minnano-AV", record["provenance"]["debut_date"])

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        resolver.OVERRIDES_FILE = os.path.join(self.temporary.name, "overrides.json")

    def tearDown(self):
        self.temporary.cleanup()

    def test_extracts_japanese_name_from_emby_labels(self):
        self.assertEqual("水城奈緒", resolver.lookup_name("[JAV_CUSTOM_PROVIDER] 水城奈緒"))
        self.assertEqual("水城奈緒", resolver.lookup_name("Nao Mizuki (JAP、1984、水城奈緒)"))
        self.assertEqual("Nao Mizuki (JAP、1984、水城奈緒)",
                         resolver.mapped_canonical_name("Nao Mizuki (JAP、1984、水城奈緒)"))

    def test_cleans_mochizuki_aliases(self):
        aliases = [
            "望月円", "もちづきまどか", "Mochizuki Madoka", "Madoka Mochizuki",
            "Also known as: En Mochizuki", "Mochizuki En",
        ]
        self.assertEqual(
            ["望月円", "もちづきまどか", "En Mochizuki"],
            resolver.clean_aliases(aliases, "En Mochizuki (JAP、2005、望月円)"),
        )

    def test_cleans_mixed_language_latin_noise(self):
        aliases = [
            "水澄ひかり", "Hikari Mizusumashi/29岁", "みすみひかり", "Misumi Hikari",
        ]
        self.assertEqual(
            ["水澄ひかり", "Hikari Misumi", "みすみひかり"],
            resolver.clean_aliases(aliases, "Hikari Misumi (JAP、1996、水澄ひかり)"),
        )

    def test_merges_nao_mizuki_identity(self):
        record = resolver.merge("水城奈緒", [
            {"id": "162", "name": "水城奈緒", "provider": "XsList",
             "homepage": "https://xslist.org/zh/model/162.html", "aliases": ["Nao Mizuki"],
             "images": ["one.jpg"], "birthday": "1984-09-17T00:00:00Z",
             "blood_type": "B", "cup_size": "G", "measurements": "B90/W58/H87",
             "nationality": "日本", "height": 159, "hobby": "", "skill": "",
             "summary": "", "debut_date": ""},
            {"id": "556877", "name": "水城奈緒", "provider": "Minnano-AV",
             "homepage": "https://www.minnano-av.com/actress556877.html",
             "aliases": ["みずきなお", "Mizuki Nao"], "images": ["two.jpg"],
             "birthday": "1984-09-18T00:00:00Z", "blood_type": "B", "cup_size": "H",
             "measurements": "B90/W65/H95", "nationality": "日本", "height": 160,
             "hobby": "華道、茶道", "skill": "", "summary": "出身地：東京都。", "debut_date": ""},
        ])
        self.assertEqual("Nao Mizuki (JAP、1984、水城奈緒)", record["name"])
        self.assertEqual("XsList:162", record["id"])
        self.assertEqual("G", record["cup_size"])
        self.assertEqual("華道、茶道", record["hobby"])
        self.assertEqual("東京都", record["place_of_birth"])
        self.assertIn("birthday", record["conflicts"])

    def test_prefers_reversed_minnano_western_name(self):
        record = resolver.merge("海老咲あお", [{
            "id": "36633", "name": "海老咲あお", "provider": "AV-LEAGUE",
            "homepage": "", "aliases": ["Ebisaki Ao"], "images": [],
            "birthday": "2000-02-02", "debut_date": "", "blood_type": "",
            "cup_size": "", "measurements": "", "nationality": "日本",
            "height": 0, "hobby": "", "skill": "", "summary": "",
        }, {
            "id": "539148", "name": "海老咲あお", "provider": "Minnano-AV",
            "homepage": "", "aliases": ["えびさきあお", "Ebisaki Ao"],
            "images": [], "birthday": "2000-09-15", "debut_date": "",
            "blood_type": "", "cup_size": "", "measurements": "",
            "nationality": "日本", "height": 0, "hobby": "", "skill": "",
            "summary": "",
        }])
        self.assertEqual("Ao Ebisaki (JAP、2000、海老咲あお)", record["name"])

    def test_reverses_javlibrary_western_name(self):
        record = resolver.merge("海老咲あお", [{
            "id": "actor", "name": "海老咲あお", "provider": "JavLibrary",
            "homepage": "", "aliases": ["Ebisaki Ao"], "images": [],
            "birthday": "2000-09-15", "debut_date": "", "blood_type": "",
            "cup_size": "", "measurements": "", "nationality": "日本",
            "height": 0, "hobby": "", "skill": "", "summary": "",
        }])
        self.assertEqual("Ao Ebisaki (JAP、2000、海老咲あお)", record["name"])

    def test_preferred_name_override_matches_any_alias(self):
        with open(resolver.OVERRIDES_FILE, "w", encoding="utf-8") as handle:
            json.dump({"actors": [{
                "canonical": "Kiho Aisaka", "original": "逢坂希穂", "birth_year": 2004,
                "aliases": ["Ousaka Kiho", "おうさかきほ"]
            }]}, handle, ensure_ascii=False)
        record = resolver.merge("逢坂希穂", [{
            "id": "37757", "name": "逢坂希穂", "provider": "AV-LEAGUE",
            "homepage": "https://www.av-league.com/actress/37757.html",
            "aliases": ["おうさかきほ", "Ousaka Kiho"], "images": [],
            "birthday": "2004-02-02T00:00:00Z", "debut_date": "", "blood_type": "",
            "cup_size": "F", "measurements": "", "nationality": "日本", "height": 157,
            "hobby": "", "skill": "", "summary": ""
        }])
        self.assertEqual("Kiho Aisaka (JAP、2004、逢坂希穂)", record["name"])

    def test_override_supplies_name_when_sources_have_no_latin_alias(self):
        with open(resolver.OVERRIDES_FILE, "w", encoding="utf-8") as handle:
            json.dump({"actors": [{
                "canonical": "Yuuka Niizuma", "original": "新妻ゆうか", "birth_year": 1993,
                "aliases": ["夕花(ゆうか)", "Niiduma Yuuka"]
            }]}, handle, ensure_ascii=False)
        record = resolver.merge("新妻ゆうか", [{
            "id": "37965", "name": "新妻ゆうか", "provider": "AV-LEAGUE",
            "homepage": "https://www.av-league.com/actress/37965.html", "aliases": [],
            "images": [], "birthday": "1993-02-02T00:00:00Z", "debut_date": "",
            "blood_type": "", "cup_size": "H", "measurements": "", "nationality": "日本",
            "height": 160, "hobby": "", "skill": "", "summary": ""
        }])
        self.assertEqual("Yuuka Niizuma (JAP、1993、新妻ゆうか)", record["name"])

    def test_aika_yumeno_override_keeps_distinct_identity(self):
        with open(resolver.OVERRIDES_FILE, "w", encoding="utf-8") as handle:
            json.dump({"actors": [{
                "canonical": "Aika Yumeno", "original": "夢乃あいか", "birth_year": 1994,
                "aliases": ["夢乃愛華", "ゆめのあいか", "Yumeno Aika"]
            }]}, handle, ensure_ascii=False)
        record = resolver.merge("夢乃あいか", [{
            "id": "9", "name": "夢乃あいか", "provider": "XsList",
            "homepage": "https://xslist.org/en/model/9.html",
            "aliases": ["ゆめのあいか", "Yumeno Aika"], "images": [],
            "birthday": "1994-08-25T00:00:00Z", "debut_date": "",
            "blood_type": "", "cup_size": "", "measurements": "",
            "nationality": "日本", "height": 0, "hobby": "", "skill": "", "summary": ""
        }])
        self.assertEqual("Aika Yumeno (JAP、1994、夢乃あいか)", record["name"])
        self.assertEqual("XsList:9", record["id"])

    def test_akane_rei_uses_latest_javlibrary_name(self):
        with open(resolver.OVERRIDES_FILE, "w", encoding="utf-8") as handle:
            json.dump({"actors": [{
                "canonical": "Rei Akane", "original": "あかね麗",
                "aliases": ["Akane Rei", "Akane Urara"],
                "historical_aliases": ["二階堂麗", "Nikaidou Urara"],
                "external_ids": {"JavLibrary Idol": "aatva"},
                "urls": {"JavLibrary Idol":
                         "https://www.javlibrary.com/en/vl_star.php?s=aatva"},
            }]}, handle, ensure_ascii=False)
        record = resolver.merge("あかね麗", [{
            "id": "251960", "name": "あかね麗", "provider": "Minnano-AV",
            "homepage": "https://www.minnano-av.com/actress251960.html",
            "aliases": ["あかねうらら", "Akane Urara"], "images": [],
            "birthday": "", "debut_date": "", "blood_type": "", "cup_size": "",
            "measurements": "", "nationality": "日本", "height": 0,
            "hobby": "", "skill": "", "summary": "",
        }])
        self.assertEqual("Rei Akane (JAP、?、あかね麗)", record["name"])
        self.assertIn("二階堂麗", record["aliases"])
        self.assertIn("Nikaidou Urara", record["aliases"])
        self.assertEqual("aatva", record["external_ids"]["JavLibrary Idol"])

    def test_resolve_searches_override_original_for_alias(self):
        with open(resolver.OVERRIDES_FILE, "w", encoding="utf-8") as handle:
            json.dump({"actors": [{
                "canonical": "Aika Yumeno", "original": "夢乃あいか", "birth_year": 1994,
                "aliases": ["夢乃愛華", "ゆめのあいか"]
            }]}, handle, ensure_ascii=False)
        resolver.CACHE_FILE = os.path.join(self.temporary.name, "cache.json")
        requested = []
        original_request_json = resolver.request_json
        original_actor_detail = resolver.actor_detail
        try:
            def request_json(url):
                requested.append(url)
                return {"data": [{
                    "id": "9", "name": "夢乃あいか", "provider": "XsList",
                    "aliases": ["ゆめのあいか"]
                }]}
            resolver.request_json = request_json
            resolver.actor_detail = lambda result: {
                **result, "homepage": "", "images": [], "birthday": "1994-08-25",
                "debut_date": "", "blood_type": "", "cup_size": "",
                "measurements": "", "nationality": "日本", "height": 0,
                "hobby": "", "skill": "", "summary": ""
            }
            record = resolver.resolve("夢乃愛華", refresh=True)
        finally:
            resolver.request_json = original_request_json
            resolver.actor_detail = original_actor_detail
        self.assertIn("q=%E5%A4%A2%E4%B9%83%E3%81%82%E3%81%84%E3%81%8B", requested[0])
        self.assertEqual("Aika Yumeno (JAP、1994、夢乃あいか)", record["name"])


if __name__ == "__main__":
    unittest.main()
