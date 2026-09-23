using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using Jellyfin.Plugin.MetaTube.Configuration;
using Jellyfin.Plugin.MetaTube.Extensions;
using Jellyfin.Plugin.MetaTube.ExternalIds;
using Jellyfin.Plugin.MetaTube.Helpers;
using Jellyfin.Plugin.MetaTube.Metadata;
using Jellyfin.Plugin.MetaTube.Translation;
using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Library;
using MediaBrowser.Controller.Entities.Movies;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Providers;
using MovieInfo = MediaBrowser.Controller.Providers.MovieInfo;
#if __EMBY__
using MediaBrowser.Model.Logging;
using MediaBrowser.Model.Configuration;
using MediaBrowser.Model.Entities;

#else
using Jellyfin.Data.Enums;
using Microsoft.Extensions.Logging;
#endif

namespace Jellyfin.Plugin.MetaTube.Providers;

#if __EMBY__
public class MovieProvider : BaseProvider, IRemoteMetadataProvider<Movie, MovieInfo>, IHasOrder, IHasMetadataFeatures
#else
public class MovieProvider : BaseProvider, IRemoteMetadataProvider<Movie, MovieInfo>, IHasOrder
#endif
{
    private const string AvBase = "AVBASE";
    private const string Gfriends = "Gfriends";
    private const string Rating = "JP-18+";

    private static readonly string[] AvBaseSupportedProviderNames = { "DUGA", "FANZA", "Getchu", "MGS" };
    private static readonly HttpClient ActorResolverClient = new() { Timeout = TimeSpan.FromSeconds(30) };
    private static readonly SemaphoreSlim PersonIndexLock = new(1, 1);
    private static IReadOnlyDictionary<string, string> _personIndex =
        new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
    private static DateTime _personIndexExpiresAt = DateTime.MinValue;

    private readonly ILibraryManager _libraryManager;

#if __EMBY__
    public MetadataFeatures[] Features => new[]
        { MetadataFeatures.Collections, MetadataFeatures.Adult, MetadataFeatures.RequiredSetup };

    public MovieProvider(ILogManager logManager, ILibraryManager libraryManager)
        : base(logManager.CreateLogger<MovieProvider>())
#else
    public MovieProvider(ILogger<MovieProvider> logger, ILibraryManager libraryManager) : base(logger)
#endif
    {
        _libraryManager = libraryManager;
    }

    public async Task<MetadataResult<Movie>> GetMetadata(MovieInfo info,
        CancellationToken cancellationToken)
    {
        var pid = info.GetPid(Plugin.ProviderId);
        if (string.IsNullOrWhiteSpace(pid.Id) || string.IsNullOrWhiteSpace(pid.Provider))
        {
            // Search movies and pick the first result.
            var firstResult = (await GetSearchResults(info, cancellationToken)).FirstOrDefault();
            if (firstResult != null) pid = firstResult.GetPid(Plugin.ProviderId);
        }

        Logger.Info("Get movie info: {0}", pid.ToString());

        var m = await ApiClient.GetMovieInfoAsync(pid.Provider, pid.Id, cancellationToken);

        // Older NFO files may contain MetaTube's former Japanese release-date
        // template. Emby's NFO reader can otherwise restore that stale tagline
        // after every full refresh. Normalize only those exact NFO fields; never
        // touch the video or unrelated metadata text.
        await TranslateLegacyReleaseDateInNfo(info.Path, cancellationToken).ConfigureAwait(false);

        // Preserve original title.
        var originalTitle = m.Title;
        var originalDirector = m.Director;

        // Convert to real actor names.
        if (Configuration.EnableRealActorNames)
            await ConvertToRealActorNames(m, cancellationToken);

        // Substitute title.
        if (Configuration.EnableTitleSubstitution)
            m.Title = Configuration.GetTitleSubstitutionTable().Substitute(m.Title);

        // Substitute genres.
        if (Configuration.EnableGenreSubstitution)
            m.Genres = Configuration.GetGenreSubstitutionTable().Substitute(m.Genres).ToArray();

        // Translate movie info.
        if (Configuration.TranslationMode != TranslationMode.Disabled)
            await TranslateMovieInfo(m, info.MetadataLanguage, cancellationToken);

        // Preserve provider actor names unless an exact custom substitution exists.
        // In particular, Japanese JavLibrary names must not be machine translated.
        if (Configuration.EnableActorSubstitution)
            m.Actors = Configuration.GetActorSubstitutionTable().Substitute(m.Actors).ToArray();

        var actorResolution = await ResolveCanonicalActorNames(m.Actors, cancellationToken).ConfigureAwait(false);
        m.Actors = actorResolution.Names;

        // Distinct and clean blank list
        m.Genres = m.Genres?.Where(x => !string.IsNullOrWhiteSpace(x)).Distinct().ToArray() ?? Array.Empty<string>();
        m.Actors = m.Actors?.Where(x => !string.IsNullOrWhiteSpace(x))
            .Select(NormalizePersonName)
            .Distinct(StringComparer.OrdinalIgnoreCase).ToArray() ?? Array.Empty<string>();

#if __EMBY__
        await using var actorCreationLock = await ActorCreationLock.AcquireAsync(m.Actors, cancellationToken)
            .ConfigureAwait(false);
#endif
        m.PreviewImages = m.PreviewImages?.Where(x => !string.IsNullOrWhiteSpace(x)).Distinct().ToArray() ??
                          Array.Empty<string>();

        // Build parameters.
        var parameters = new Dictionary<string, string>
        {
            { @"{provider}", m.Provider },
            { @"{id}", m.Id },
            { @"{number}", m.Number },
            { @"{title}", m.Title },
            { @"{series}", m.Series },
            { @"{maker}", m.Maker },
            { @"{label}", m.Label },
            { @"{director}", m.Director },
            { @"{actors}", m.Actors?.Any() == true ? string.Join(' ', m.Actors) : string.Empty },
            { @"{first_actor}", m.Actors?.FirstOrDefault() },
            { @"{year}", $"{m.ReleaseDate:yyyy}" },
            { @"{month}", $"{m.ReleaseDate:MM}" },
            { @"{date}", $"{m.ReleaseDate:yyyy-MM-dd}" }
        };

        var result = new MetadataResult<Movie>
        {
            Item = new Movie
            {
                Name = RenderTemplate(
                    Configuration.EnableTemplate
                        ? Configuration.NameTemplate
                        : PluginConfiguration.DefaultNameTemplate, parameters),
                Tagline = RenderTemplate(
                    Configuration.EnableTemplate
                        ? Configuration.TaglineTemplate
                        : PluginConfiguration.DefaultTaglineTemplate, parameters),
                OriginalTitle = originalTitle,
                Overview = m.Summary,
                OfficialRating = Rating,
                PremiereDate = m.ReleaseDate.GetValidDateTime(),
                ProductionYear = m.ReleaseDate.GetValidYear(),
                Genres = m.Genres?.Any() == true ? m.Genres : Array.Empty<string>()
            },
            HasMetadata = true
        };

        // Set provider id.
        result.Item.SetPid(Name, m.Provider, m.Id, pid.Position);

        // JavTrailers is a separate, plugin-local fallback. It uses MetaTube's
        // own FlareSolverr and never calls JAV Master. Prefer its HLS trailer
        // when available, then retain the normal MetaTube preview as a fallback.
        var trailerUrl = !string.IsNullOrWhiteSpace(m.PreviewVideoUrl)
            ? m.PreviewVideoUrl
            : m.PreviewVideoHlsUrl;
        var trailerImageUrl = string.Empty;
        if (Configuration.EnableJavTrailers)
        {
            try
            {
                var trailer = await JavTrailersClient.GetTrailerAsync(
                    m.Number, Configuration.JavTrailersSolverUrl, cancellationToken).ConfigureAwait(false);
                if (trailer != null)
                {
                    trailerUrl = trailer.VideoUrl;
                    if (!string.IsNullOrWhiteSpace(trailer.ImageUrl))
                    {
                        trailerImageUrl = trailer.ImageUrl;
                        result.Item.SetTrailerImageUrl(trailer.ImageUrl);
                    }
                    Logger.Info("Resolved JavTrailers trailer for {0}", m.Number);
                }
            }
            catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException
                                              or JsonException or InvalidOperationException)
            {
                Logger.Warn("JavTrailers trailer lookup failed for {0}: {1}", m.Number, exception.Message);
            }
        }
        if (!string.IsNullOrWhiteSpace(trailerUrl))
        {
            result.Item.SetTrailerUrl(trailerUrl);
        }
        if (Configuration.EnableWindmillTrailerJobs)
        {
            try
            {
                var jobId = await WindmillTrailerClient.SubmitAsync(
                    Configuration.WindmillUrl, Configuration.WindmillWorkspace,
                    Configuration.WindmillTrailerScript, Configuration.WindmillToken,
                    m.Number, trailerUrl ?? string.Empty, trailerImageUrl, cancellationToken).ConfigureAwait(false);
                Logger.Info("Submitted Windmill trailer job {0} for {1}", jobId, m.Number);
            }
            catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException
                                              or JsonException or InvalidOperationException)
            {
                Logger.Warn("Submit Windmill trailer job for {0} failed: {1}", m.Number, exception.Message);
            }
        }

        // Set community rating.
        if (Configuration.EnableRatings)
            result.Item.CommunityRating = m.Score > 0 ? (float)Math.Round(m.Score * 2, 1) : null;

        // Add collection.
        if (Configuration.EnableCollections && !string.IsNullOrWhiteSpace(m.Series))
        {
            result.Item.AddCollection(m.Series);
            Logger.Info("Add Collection for movie {0} [{1}]", pid.ToString(), m.Series);
        }

        // Add studio.
        if (!string.IsNullOrWhiteSpace(m.Maker))
            result.Item.AddStudio(m.Maker);

        // Add tag (series).
        if (!string.IsNullOrWhiteSpace(m.Series))
            result.Item.AddTag(m.Series);

        // Add tag (label).
        if (!string.IsNullOrWhiteSpace(m.Label))
            result.Item.AddTag(m.Label);

        // Store the director as a movie field. Do not create a director Person,
        // which would make the name appear in Emby's Cast & Crew/People section.
        if (!string.IsNullOrWhiteSpace(originalDirector))
            result.Item.SetProviderId(DirectorExternalId.ProviderKey, originalDirector);

        // Expose the translated label as a dedicated movie field as well as a tag.
        if (!string.IsNullOrWhiteSpace(m.Label))
            result.Item.SetProviderId(LabelExternalId.ProviderKey, m.Label);

        // Add actors.
        foreach (var name in m.Actors ?? Enumerable.Empty<string>())
        {
            var actor = new PersonInfo
            {
                Name = name,
#if __EMBY__
                Type = PersonType.Actor,
#else
                Type = PersonKind.Actor,
#endif
            };
            if (!actorResolution.ExistingNames.Contains(name))
                await SetActorImageUrl(actor, cancellationToken);
            result.AddPerson(actor);
        }

        MetadataRefreshTracker.Mark(m.Provider, m.Id);

        return result;
    }

    private async Task TranslateLegacyReleaseDateInNfo(string mediaPath, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(mediaPath)) return;

        var nfoPath = Path.ChangeExtension(mediaPath, ".nfo");
        if (!File.Exists(nfoPath)) return;

        try
        {
            var current = await File.ReadAllTextAsync(nfoPath, cancellationToken).ConfigureAwait(false);
            var updated = Regex.Replace(
                current,
                @"(<outline><!\[CDATA\[|<tagline>)配信開始日 (?<date>\d{4}-\d{2}-\d{2})(\]\]></outline>|</tagline>)",
                @"$1Release Date ${date}$3",
                RegexOptions.CultureInvariant);

            if (string.Equals(current, updated, StringComparison.Ordinal)) return;

            await File.WriteAllTextAsync(nfoPath, updated, Encoding.UTF8, cancellationToken)
                .ConfigureAwait(false);
            Logger.Info("Translated legacy release-date tagline in {0}", nfoPath);
        }
        catch (Exception exception)
        {
            Logger.Warn("Unable to translate release-date tagline in {0}: {1}", nfoPath, exception.Message);
        }
    }

    private async Task<(string[] Names, HashSet<string> ExistingNames)> ResolveCanonicalActorNames(
        IEnumerable<string> actors,
        CancellationToken cancellationToken)
    {
        var names = actors?.Where(name => !string.IsNullOrWhiteSpace(name)).ToArray() ?? Array.Empty<string>();
        var existingNames = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        if (names.Length == 0) return (names, existingNames);

        var personIndex = Configuration.ReuseExistingEmbyActors
            ? await GetPersonIndex(cancellationToken).ConfigureAwait(false)
            : new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

        var resolved = new List<string>(names.Length);
        foreach (var name in names)
        {
            var existingName = ActorIdentityKeys(name)
                .Select(key => personIndex.TryGetValue(key, out var match) ? match : null)
                .FirstOrDefault(match => !string.IsNullOrWhiteSpace(match));
            if (!string.IsNullOrWhiteSpace(existingName))
            {
                resolved.Add(existingName);
                existingNames.Add(NormalizePersonName(existingName));
                Logger.Debug("Reuse existing Emby actor without online lookup: {0} -> {1}", name, existingName);
                continue;
            }

            if (string.IsNullOrWhiteSpace(Configuration.ActorResolverUrl))
            {
                resolved.Add(name);
                continue;
            }

            try
            {
                var url = $"{Configuration.ActorResolverUrl.TrimEnd('/')}/resolve?name={Uri.EscapeDataString(name)}";
                await using var stream = await ActorResolverClient.GetStreamAsync(url, cancellationToken).ConfigureAwait(false);
                using var document = await JsonDocument.ParseAsync(stream, cancellationToken: cancellationToken)
                    .ConfigureAwait(false);
                var canonical = document.RootElement.GetProperty("data").GetProperty("name").GetString();
                resolved.Add(string.IsNullOrWhiteSpace(canonical) ? name : canonical);
            }
            catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException
                                              or JsonException or KeyNotFoundException)
            {
                Logger.Warn("Actor resolver failed for {0}: {1}", name, exception.Message);
                resolved.Add(name);
            }
        }

        return (resolved.Distinct(StringComparer.OrdinalIgnoreCase).ToArray(), existingNames);
    }

    private async Task<IReadOnlyDictionary<string, string>> GetPersonIndex(CancellationToken cancellationToken)
    {
        if (_personIndexExpiresAt > DateTime.UtcNow) return _personIndex;

        await PersonIndexLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_personIndexExpiresAt > DateTime.UtcNow) return _personIndex;

            var index = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            var people = _libraryManager.GetItemList(new InternalItemsQuery
            {
#if __EMBY__
                IncludeItemTypes = new[] { nameof(Person) },
#else
                IncludeItemTypes = new[] { Jellyfin.Data.Enums.BaseItemKind.Person },
#endif
                Recursive = true
            });
            foreach (var person in people.Where(person => !string.IsNullOrWhiteSpace(person.Name)))
            {
                AddPersonIdentity(index, person.Name, person.Name);
                if (!string.IsNullOrWhiteSpace(person.OriginalTitle))
                    AddPersonIdentity(index, person.OriginalTitle, person.Name);
                foreach (var providerId in person.ProviderIds)
                {
                    if (providerId.Key.Equals("JAVAlsoKnownAs", StringComparison.OrdinalIgnoreCase))
                    {
                        foreach (var alias in providerId.Value.Split(',', StringSplitOptions.RemoveEmptyEntries))
                            AddPersonIdentity(index, alias, person.Name);
                    }
                }
            }

            _personIndex = index;
            _personIndexExpiresAt = DateTime.UtcNow.AddMinutes(5);
            Logger.Info("Indexed {0} existing Emby actor identities", index.Count);
            return _personIndex;
        }
        finally
        {
            PersonIndexLock.Release();
        }
    }

    private static void AddPersonIdentity(IDictionary<string, string> index, string identity, string preferredName)
    {
        foreach (var key in ActorIdentityKeys(identity)) index.TryAdd(key, preferredName);
    }

    private static IEnumerable<string> ActorIdentityKeys(string value)
    {
        value = NormalizePersonName(value);
        if (value.Length == 0) yield break;

        yield return value;
        var canonical = Regex.Match(value, @"^(.*?)\s*\(JAP、(?:\d{4}|\?)、([^()]+)\)\s*$",
            RegexOptions.IgnoreCase);
        if (canonical.Success)
        {
            foreach (var key in ActorIdentityKeys(canonical.Groups[1].Value)) yield return key;
            foreach (var key in ActorIdentityKeys(canonical.Groups[2].Value)) yield return key;
            yield break;
        }

        if (!Regex.IsMatch(value, @"[\u3040-\u30ff\u3400-\u9fff]"))
        {
            var words = value.Split(' ', StringSplitOptions.RemoveEmptyEntries);
            if (words.Length == 2) yield return $"{words[1]} {words[0]}";
        }
    }

    public async Task<IEnumerable<RemoteSearchResult>> GetSearchResults(MovieInfo info,
        CancellationToken cancellationToken)
    {
        var pid = info.GetPid(Plugin.ProviderId);

        var searchResults = new List<MovieSearchResult>();
        if (string.IsNullOrWhiteSpace(pid.Id) || string.IsNullOrWhiteSpace(pid.Provider))
        {
            // Search movie by name.
            Logger.Info("Search for movie: {0}", info.Name);
            searchResults.AddRange(await ApiClient.SearchMovieAsync(info.Name, pid.Provider, cancellationToken));
        }
        else
        {
            // Exact search.
            Logger.Info("Search for movie: {0}", pid.ToString());
            searchResults.Add(await ApiClient.GetMovieInfoAsync(pid.Provider, pid.Id,
                pid.Update != true, cancellationToken));
        }

        if (Configuration.EnableMovieProviderFilter)
        {
            if (Configuration.GetMovieProviderFilter() is { } filter &&
                filter.Any()) // Apply only if filter is not empty.
            {
                // Filter out mismatched results.
                searchResults.RemoveAll(m => !filter.Contains(m.Provider, StringComparer.OrdinalIgnoreCase));
                // Reorder results by stable sort.
                searchResults = searchResults.OrderBy(m =>
                    filter.FindIndex(s => s.Equals(m.Provider, StringComparison.OrdinalIgnoreCase))).ToList();
            }
            else
            {
                Logger.Warn("Movie provider filter enabled but never used");
            }
        }

        var results = new List<RemoteSearchResult>();
        if (!searchResults.Any())
        {
            Logger.Warn("Movie not found or has been filtered: {0}", pid.Id);
            return results;
        }

        foreach (var m in searchResults)
        {
            var result = new RemoteSearchResult
            {
                Name = $"[{m.Provider}] {m.Number} {m.Title}",
                SearchProviderName = Name,
                PremiereDate = m.ReleaseDate.GetValidDateTime(),
                ProductionYear = m.ReleaseDate.GetValidYear(),
                ImageUrl = ApiClient.GetPrimaryImageApiUrl(m.Provider, m.Id, m.ThumbUrl, 1.0, true)
            };
            result.SetPid(Name, m.Provider, m.Id, pid.Position);
            results.Add(result);
        }

        return results;
    }

    private async Task SetActorImageUrl(PersonInfo actor, CancellationToken cancellationToken)
    {
        try
        {
            var results = await ApiClient.SearchActorAsync(actor.Name, cancellationToken);
            if (results?.Any() != true)
            {
                Logger.Warn("Actor not found: {0}", actor.Name);
                return;
            }

            // Use the first result as the primary actor selection.
            var firstResult = results.First();
            if (firstResult.Images?.Any() == true)
            {
                actor.ImageUrl = ApiClient.GetPrimaryImageApiUrl(
                    firstResult.Provider, firstResult.Id, firstResult.Images.First(), 0.5, true);
                actor.SetPid(Name, firstResult.Provider, firstResult.Id);
            }

            // Use the Gfriends to update the actor profile image, if any.
            foreach (var result in results.Where(result => result.Provider == Gfriends && result.Images?.Any() == true))
            {
                actor.ImageUrl = ApiClient.GetPrimaryImageApiUrl(
                    result.Provider, result.Id, result.Images.First(), 0.5, true);
            }
        }
        catch (Exception e)
        {
            Logger.Error("Get actor image error: {0} ({1})", actor.Name, e.Message);
        }
    }

    private static string NormalizePersonName(string name)
    {
        return string.Join(' ', name.Normalize().Split((char[])null,
            StringSplitOptions.RemoveEmptyEntries));
    }

    private async Task ConvertToRealActorNames(MovieSearchResult m, CancellationToken cancellationToken)
    {
        if (!AvBaseSupportedProviderNames.Contains(m.Provider, StringComparer.OrdinalIgnoreCase)) return;

        try
        {
            var searchResults = await ApiClient.SearchMovieAsync(m.Id, AvBase, cancellationToken);
            if (searchResults?.Any() != true)
            {
                Logger.Warn("Movie not found on AVBASE: {0}", m.Id);
                return;
            }

            foreach (var result in searchResults)
            {
                var similarity = CalculateTitleSimilarity(m, result);

                Logger.Info("Calculate movie title similarity for {0} ({1}) and {2} ({3}): {4:0.00%}",
                    m.Id, m.Provider, result.Id, result.Provider, similarity);

                if (similarity >= 0.8)
                {
                    if (result.Actors?.Any() == true)
                        m.Actors = result.Actors;
                    return;
                }
            }

            Logger.Warn("No matching movie found on AVBASE for {0}", m.Id);
        }
        catch (Exception e)
        {
            Logger.Error("Convert to real actor names error: {0} ({1})", m.Number, e.Message);
        }
    }

    private static double CalculateTitleSimilarity(MovieSearchResult source, MovieSearchResult target)
    {
        var sourceKey = Normalize(source.Number + source.Title);
        var targetKey = Normalize(target.Number + target.Title);

        if (string.IsNullOrWhiteSpace(sourceKey) || string.IsNullOrWhiteSpace(targetKey))
            return 0.0;

        var distance = Levenshtein.Distance(sourceKey, targetKey);
        var avgLength = (sourceKey.Length + targetKey.Length) / 2.0;
        var similarity = 1.0 - distance / avgLength;

        return Math.Clamp(similarity, 0.0, 1.0);

        string Normalize(string s)
        {
            if (string.IsNullOrWhiteSpace(s))
                return string.Empty;

            s = s.ToLowerInvariant();
            s = Regex.Replace(s, @"[\s\[\]\(\)【】（）]", "");
            return s.Trim();
        }
    }

    private async Task TranslateMovieInfo(Metadata.MovieInfo m, string language, CancellationToken cancellationToken)
    {
        try
        {
            Logger.Info("Translate movie info language: {0} => {1}", m.Number, language);
            await TranslationHelper.TranslateAsync(m, language, cancellationToken);
        }
        catch (Exception e)
        {
            Logger.Error("Translate error: {0}", e.Message);
        }
    }

    private static string RenderTemplate(string template, Dictionary<string, string> parameters)
    {
        if (string.IsNullOrWhiteSpace(template))
            return string.Empty;

        var sb = parameters.Where(kvp => template.Contains(kvp.Key))
            .Aggregate(new StringBuilder(template),
                (sb, kvp) => sb.Replace(kvp.Key, kvp.Value));

        return sb.ToString().Trim();
    }
}
