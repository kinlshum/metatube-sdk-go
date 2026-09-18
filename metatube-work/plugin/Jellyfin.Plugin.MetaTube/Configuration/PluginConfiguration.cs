using Jellyfin.Plugin.MetaTube.Helpers;
using Jellyfin.Plugin.MetaTube.Translation;
#if __EMBY__
using System.ComponentModel;
using Emby.Web.GenericEdit;
using MediaBrowser.Model.Attributes;

#else
using MediaBrowser.Model.Plugins;
#endif

namespace Jellyfin.Plugin.MetaTube.Configuration;

#if __EMBY__
public class PluginConfiguration : EditableOptionsBase
{
    public override string EditorTitle => Plugin.ProviderName;
#else
public class PluginConfiguration : BasePluginConfiguration
{
#endif

#if __EMBY__
    [DisplayName("Server")]
    [Description("Full url of the MetaTube Server, HTTPS protocol is recommended.")]
    [Required]
#endif
    public string Server { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Token")]
    [Description("Access token for the MetaTube Server, or blank if no token is set by the backend.")]
#endif
    public string Token { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Enable auto update")]
    [Description("Automatically update the plugin through scheduled tasks.")]
    public bool EnableAutoUpdate { get; set; } = false;
#endif

#if __EMBY__
    [DisplayName("Enable collections")]
    [Description("Automatically create collections by series.")]
#endif
    public bool EnableCollections { get; set; } = false;

#if __EMBY__
    [DisplayName("Legacy director option")]
    [Description("Retained for configuration compatibility. Directors are now stored only in the Director movie field.")]
#endif
    public bool EnableDirectors { get; set; } = true;

#if __EMBY__
    [DisplayName("Enable ratings")]
    [Description("Display community ratings from the original website.")]
#endif
    public bool EnableRatings { get; set; } = true;

#if __EMBY__
    [DisplayName("Enable trailers")]
    [Description("Generate online video trailers in strm format.")]
#endif
    public bool EnableTrailers { get; set; } = false;

#if __EMBY__
    [DisplayName("Download trailers locally")]
    [Description("Save each resolved trailer as a local MP4 in the movie's trailers folder. A streaming .strm is kept only if the download fails.")]
#endif
    public bool DownloadTrailersLocally { get; set; } = true;

#if __EMBY__
    [DisplayName("Enable JavTrailers fallback")]
    [Description("Resolve a JavTrailers HLS trailer through MetaTube's own FlareSolverr when available.")]
#endif
    public bool EnableJavTrailers { get; set; } = true;

#if __EMBY__
    [DisplayName("JavTrailers FlareSolverr URL")]
    [Description("The MetaTube project's private FlareSolverr API endpoint.")]
#endif
    public string JavTrailersSolverUrl { get; set; } = "http://192.168.10.170:8191/v1";

#if __EMBY__
    [DisplayName("Submit trailer jobs to Windmill")]
    [Description("Create a visible Windmill job after MetaTube resolves a trailer during a movie refresh.")]
#endif
    public bool EnableWindmillTrailerJobs { get; set; } = true;

#if __EMBY__
    [DisplayName("Windmill URL")]
    [Description("Base URL of MetaTube's Windmill instance.")]
#endif
    public string WindmillUrl { get; set; } = "http://192.168.10.170:8001";

#if __EMBY__
    [DisplayName("Windmill workspace")]
#endif
    public string WindmillWorkspace { get; set; } = "admins";

#if __EMBY__
    [DisplayName("Windmill trailer script")]
#endif
    public string WindmillTrailerScript { get; set; } = "u/admin/metatube_trailer";

#if __EMBY__
    [DisplayName("Windmill token")]
    [Description("Bearer token used only to submit MetaTube trailer jobs.")]
#endif
    public string WindmillToken { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Enable real actor names")]
    [Description("Search and replace with real actor names from AVBASE.")]
#endif
    public bool EnableRealActorNames { get; set; } = false;

#if __EMBY__
    [DisplayName("Expose MetaTube actor provider")]
    [Description("Show individual MetaTube actor-source results in Emby. Disable when JAV_CUSTOM_PROVIDER performs merged actor lookup.")]
#endif
    public bool EnableActorProvider { get; set; } = false;

#if __EMBY__
    [DisplayName("Actor resolver URL")]
    [Description("Resolve movie cast aliases to canonical actor names during title lookup.")]
#endif
    public string ActorResolverUrl { get; set; } = "http://192.168.10.170:9211";

#if __EMBY__
    [DisplayName("Reuse existing Emby actors")]
    [Description("During movie scans, match existing Emby people by preferred name, Japanese name, aliases, or reversed English name and skip online actor and image lookups.")]
#endif
    public bool ReuseExistingEmbyActors { get; set; } = true;

#if __EMBY__
    [DisplayName("Enable badges")]
    [Description("Add Chinese subtitle badges to primary images.")]
#endif
    public bool EnableBadges { get; set; } = false;

#if __EMBY__
    [DisplayName("Badge url")]
    [Description("Custom badge url, PNG format is recommended. (default: zimu.png)")]
#endif
    public string BadgeUrl { get; set; } = "zimu.png";

#if __EMBY__
    [DisplayName("Primary image ratio")]
    [Description("Aspect ratio for primary images, set a negative value to use the default.")]
#endif
    public double PrimaryImageRatio { get; set; } = -1;

#if __EMBY__
    [DisplayName("Default image quality")]
    [Description("Default compression quality for JPEG images, set between 0 and 100. (default: 90)")]
    [MinValue(0)]
    [MaxValue(100)]
    [Required]
#endif
    public int DefaultImageQuality { get; set; } = 90;

#if __EMBY__
    [DisplayName("Save all backdrops beside media")]
    [Description("Download every movie backdrop as fanart.jpg, fanart1.jpg, fanart2.jpg, and so on.")]
#endif
    public bool SaveAllBackdropsLocally { get; set; } = true;

#if __EMBY__
    [DisplayName("Enable movie provider filter")]
    [Description("Filter and reorder search results from movie providers.")]
#endif
    public bool EnableMovieProviderFilter { get; set; } = false;

#if __EMBY__
    [DisplayName("Movie provider filter")]
    [Description(
        "Provider names are case-insensitive, with decreasing precedence from left to right, separated by commas.")]
#endif
    public string RawMovieProviderFilter
    {
        get => _movieProviderFilter?.Any() == true ? string.Join(',', _movieProviderFilter) : string.Empty;
        set => _movieProviderFilter = value?.Split(',').Select(s => s.Trim()).Where(s => s.Any())
            .Distinct(StringComparer.OrdinalIgnoreCase).ToList();
    }

    public List<string> GetMovieProviderFilter()
    {
        return _movieProviderFilter;
    }

    private List<string> _movieProviderFilter;

#if __EMBY__
    [DisplayName("Enable template")]
#endif
    public bool EnableTemplate { get; set; } = false;

#if __EMBY__
    [DisplayName("Name template")]
#endif
    public string NameTemplate { get; set; } = DefaultNameTemplate;

#if __EMBY__
    [DisplayName("Tagline template")]
#endif
    public string TaglineTemplate { get; set; } = DefaultTaglineTemplate;

    public static string DefaultNameTemplate => "{number} {title}";

    public static string DefaultTaglineTemplate => "Release Date {date}";

#if __EMBY__
    [DisplayName("Translation mode")]
#endif
    public TranslationMode TranslationMode { get; set; } = TranslationMode.Disabled;

#if __EMBY__
    [DisplayName("Translation engine")]
#endif
    public TranslationEngine TranslationEngine { get; set; } = TranslationEngine.Baidu;

#if __EMBY__
    [DisplayName("Baidu app id")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.Baidu)]
#endif
    public string BaiduAppId { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Baidu app key")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.Baidu)]
#endif
    public string BaiduAppKey { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Google api key")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.Google)]
#endif
    public string GoogleApiKey { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Google api url")]
    [Description("Custom Google translate api url. (optional)")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.Google)]
#endif
    public string GoogleApiUrl { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("DeepL api key")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.DeepL)]
#endif
    public string DeepLApiKey { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("DeepL api url")]
    [Description("Custom DeepL-compatible api url. (optional)")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.DeepL)]
#endif
    public string DeepLApiUrl { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("OpenAI api key")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.OpenAi)]
#endif
    public string OpenAiApiKey { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("OpenAI api url")]
    [Description("Custom OpenAI-compatible api url. (optional)")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.OpenAi)]
#endif
    public string OpenAiApiUrl { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("OpenAI model")]
    [Description("Custom OpenAI-compatible api model. (optional)")]
    [VisibleCondition(nameof(TranslationEngine), ValueCondition.IsEqual, TranslationEngine.OpenAi)]
#endif
    public string OpenAiModel { get; set; } = string.Empty;

#if __EMBY__
    [DisplayName("Enable title substitution")]
#endif
    public bool EnableTitleSubstitution { get; set; } = false;

#if __EMBY__
    [DisplayName("Title substitution table")]
    [Description(
        "One record per line, separated by equal signs. Leave the target substring blank to delete the source substring.")]
    [EditMultiline(5)]
#endif
    public string TitleRawSubstitutionTable
    {
        get => _titleSubstitutionTable?.ToString();
        set => _titleSubstitutionTable = SubstitutionTable.Parse(value);
    }

    public SubstitutionTable GetTitleSubstitutionTable()
    {
        return _titleSubstitutionTable;
    }

    private SubstitutionTable _titleSubstitutionTable;

#if __EMBY__
    [DisplayName("Enable actor substitution")]
#endif
    public bool EnableActorSubstitution { get; set; } = false;

#if __EMBY__
    [DisplayName("Actor substitution table")]
    [Description(
        "One record per line, separated by equal signs. Leave the target actor blank to delete the source actor.")]
    [EditMultiline(5)]
#endif
    public string ActorRawSubstitutionTable
    {
        get => _actorSubstitutionTable?.ToString();
        set => _actorSubstitutionTable = SubstitutionTable.Parse(value);
    }

    public SubstitutionTable GetActorSubstitutionTable()
    {
        return _actorSubstitutionTable;
    }

    private SubstitutionTable _actorSubstitutionTable;

#if __EMBY__
    [DisplayName("Enable genre substitution")]
#endif
    public bool EnableGenreSubstitution { get; set; } = false;

#if __EMBY__
    [DisplayName("Title substitution table")]
    [Description(
        "One record per line, separated by equal signs. Leave the target genre blank to delete the source genre.")]
    [EditMultiline(5)]
#endif
    public string GenreRawSubstitutionTable
    {
        get => _genreSubstitutionTable?.ToString();
        set => _genreSubstitutionTable = SubstitutionTable.Parse(value);
    }

    public SubstitutionTable GetGenreSubstitutionTable()
    {
        return _genreSubstitutionTable;
    }

    private SubstitutionTable _genreSubstitutionTable;
}
