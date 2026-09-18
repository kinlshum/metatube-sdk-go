using MediaBrowser.Controller.Entities.Movies;
using MediaBrowser.Model.Entities;
#if !__EMBY__
using MediaBrowser.Model.Providers;
#endif

namespace Jellyfin.Plugin.MetaTube.ExternalIds;

public class MovieExternalId : BaseExternalId
{
#if !__EMBY__
    public override ExternalIdMediaType? Type => ExternalIdMediaType.Movie;
#endif

    public override bool Supports(IHasProviderIds item)
    {
        return item is Movie;
    }
}

/// <summary>
/// Stores the scraped director as movie metadata without creating an Emby Person.
/// </summary>
public sealed class DirectorExternalId : BaseExternalId
{
    public const string ProviderKey = "JAVDirector";

#if __EMBY__
    public override string Name => "Director";
#else
    public override string ProviderName => "Director";
    public override ExternalIdMediaType? Type => ExternalIdMediaType.Movie;
#endif

    public override string Key => ProviderKey;

    public override string UrlFormatString => "https://www.avbase.net/works?q={0}";

    public override bool Supports(IHasProviderIds item)
    {
        return item is Movie;
    }
}

/// <summary>
/// Exposes the translated provider label as a dedicated movie field.
/// </summary>
public sealed class LabelExternalId : BaseExternalId
{
    public const string ProviderKey = "JAVLabel";

#if __EMBY__
    public override string Name => "Label";
#else
    public override string ProviderName => "Label";
    public override ExternalIdMediaType? Type => ExternalIdMediaType.Movie;
#endif

    public override string Key => ProviderKey;

    public override string UrlFormatString => "https://www.avbase.net/works?q={0}";

    public override bool Supports(IHasProviderIds item)
    {
        return item is Movie;
    }
}
