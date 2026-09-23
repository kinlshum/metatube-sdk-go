using System.ComponentModel;
using Emby.Web.GenericEdit;
using MediaBrowser.Model.Attributes;

namespace JAV.Custom.Provider.Configuration;

public sealed class PluginConfiguration : EditableOptionsBase
{
    public override string EditorTitle => Plugin.ProviderName;

    [DisplayName("Custom actor records (JSON)")]
    [Description("One JSON array. Stable id and name are required. Aliases resolve to the same actor. Keep URLs and provider-specific IDs in urls and externalIds.")]
    [EditMultiline(28)]
    public string RecordsJson { get; set; } = DefaultRecords;

    [DisplayName("Enable online actor lookup")]
    [Description("Fill blank custom fields from the configured MetaTube actor sources during Identify and Refresh. Manual record values always win.")]
    public bool EnableOnlineLookup { get; set; } = true;

    [DisplayName("MetaTube server")]
    public string MetaTubeServer { get; set; } = "http://192.168.10.166:8080";

    [DisplayName("MetaTube token")]
    public string MetaTubeToken { get; set; } = string.Empty;

    [DisplayName("Actor resolver")]
    [Description("Standalone service that merges actor sources and returns a canonical identity.")]
    public string ActorResolverUrl { get; set; } = "http://192.168.10.170:9211";

    [DisplayName("Actor source order")]
    [Description("Only exact-name results from these providers are merged, in this order.")]
    public string ActorSourceOrder { get; set; } = "AV-LEAGUE,XsList,Minnano-AV,Gfriends";

    [DisplayName("Custom ID 1 name")]
    [Description("Shown in Emby's People external-ID editor.")]
    public string CustomId1Name { get; set; } = "Superjav";

    [DisplayName("Custom ID 1 link")]
    [Description("Use {0} where the saved ID belongs.")]
    public string CustomId1Url { get; set; } = "https://supjav.com/category/cast/{0}";

    [DisplayName("Custom ID 2 name")]
    public string CustomId2Name { get; set; } = "Javdb";

    [DisplayName("Custom ID 2 link")]
    [Description("Use {0} where the saved ID belongs.")]
    public string CustomId2Url { get; set; } = "https://javdb.com/actors/{0}.html";

    public static string DefaultRecords => """
[
  {
    "id": "aika-1990",
    "name": "AIKA (JAP、1990、あいか)",
    "aliases": ["AIKA", "あいか", "佐藤聖羅", "優木あいか", "平岡歩", "本田愛華", "綾瀬愛花", "西野あおい", "香川さくら", "香川さくら/あいか"],
    "birthday": "1990-08-24",
    "deathDate": "",
    "debutDate": "2011-01-01",
    "debutAge": 19,
    "debutTitle": "Deceptive Shooting Generation 11 Aika-chan, 20 years old",
    "embyMovieCount": 738,
    "javDbMovieCount": 1583,
    "placeOfBirth": "兵庫県",
    "nationality": "Japanese",
    "measurements": "B87 / W60 / H84 | T165 / B86(F Cup) / W60 / H84 / S",
    "cupSize": "D Cup",
    "avActivity": "active",
    "sign": "Virgo",
    "bloodType": "O",
    "height": "163cm",
    "agency": "T-POWERS",
    "hobbies": "Dance, Fashion",
    "avAppearancePeriod": "2011 -",
    "overview": "",
    "tags": ["beautiful breasts", "Black Gal", "Big breasts", "beautiful woman", "Tokyo Heat", "pie pan", "Big ass", "Uncensored", "beautiful girl", "Delivery Health Girl"],
    "externalIds": {
      "AV-LEAGUE": "18",
      "XsList": "20",
      "SupjavActor": "AIKA",
      "JavdbActor": "RXbR",
      "InstagramActor": "aika_honmono",
      "JavLibraryIdol": "anha",
      "MetaTube": "AV-LEAGUE:18",
      "SextbActress": "AIKA",
      "StashActor": "test",
      "TwitterActor": "aika50"
    },
    "urls": {
      "XsList": "https://xslist.org/en/model/20.html",
      "X": "https://x.com/aika_honmono",
      "Official website": "http://www.t-powers.co.jp/official/talent/aika/"
    },
    "imageUrls": []
  }
]
""";
}
