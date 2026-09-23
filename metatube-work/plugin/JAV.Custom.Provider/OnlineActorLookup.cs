using System.Net.Http.Headers;
using System.Text.Json;
using JAV.Custom.Provider.Models;
using MediaBrowser.Model.Logging;

namespace JAV.Custom.Provider;

internal static class OnlineActorLookup
{
    private static readonly HttpClient HttpClient = new() { Timeout = TimeSpan.FromSeconds(90) };
    private static readonly JsonSerializerOptions JsonOptions = new() { PropertyNameCaseInsensitive = true };

    public static async Task<IReadOnlyList<RemoteActor>> Lookup(ActorRecord record, ILogger logger, CancellationToken cancellationToken)
    {
        var configuration = Plugin.Instance.Configuration;
        if (!configuration.EnableOnlineLookup) return Array.Empty<RemoteActor>();

        try
        {
            var identities = record.Aliases.Append(record.Name)
                .Where(value => !string.IsNullOrWhiteSpace(value))
                .Distinct(StringComparer.OrdinalIgnoreCase)
                .ToArray();
            var sourceOrder = configuration.ActorSourceOrder.Split(',')
                .Select(value => value.Trim())
                .Where(value => value.Length > 0)
                .ToList();
            var selected = KnownActors(record, sourceOrder).ToList();
            var selectedProviders = selected.Select(actor => actor.Provider).ToHashSet(StringComparer.OrdinalIgnoreCase);
            var query = identities.FirstOrDefault(ContainsJapanese)
                        ?? identities.FirstOrDefault()
                        ?? string.Empty;
            if (query.Length == 0) return Array.Empty<RemoteActor>();

            if (!string.IsNullOrWhiteSpace(configuration.ActorResolverUrl))
            {
                try
                {
                    var resolverUrl = $"{configuration.ActorResolverUrl.TrimEnd('/')}/resolve?name={Uri.EscapeDataString(query)}";
                    var resolved = await Get<ApiEnvelope<RemoteActor>>(resolverUrl, string.Empty, cancellationToken).ConfigureAwait(false);
                    if (resolved.Data is not null) return new[] { resolved.Data };
                }
                catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or JsonException)
                {
                    logger.ErrorException("Actor resolver failed; falling back to MetaTube sources for " + record.Name, exception);
                }
            }

            var searchUrl = $"{configuration.MetaTubeServer.TrimEnd('/')}/v1/actors/search?q={Uri.EscapeDataString(query)}&fallback=true";
            var search = await Get<ApiEnvelope<List<RemoteActorSearch>>>(searchUrl, configuration.MetaTubeToken, cancellationToken).ConfigureAwait(false);
            var matches = (search.Data ?? new List<RemoteActorSearch>())
                .Where(actor => Matches(actor, identities))
                .GroupBy(actor => actor.Provider, StringComparer.OrdinalIgnoreCase)
                .Select(group => SelectUnambiguous(group, identities))
                .Where(actor => actor is not null)
                .Cast<RemoteActorSearch>();
            selected.AddRange(matches.Where(actor => selectedProviders.Add(actor.Provider)));
            selected = selected.OrderBy(actor => SourceRank(actor.Provider, sourceOrder))
                .ThenBy(actor => actor.Provider, StringComparer.OrdinalIgnoreCase)
                .ToList();

            var tasks = selected.Select(actor => TryGetActor(
                $"{configuration.MetaTubeServer.TrimEnd('/')}/v1/actors/{Uri.EscapeDataString(actor.Provider)}/{Uri.EscapeDataString(actor.Id)}?lazy=false",
                configuration.MetaTubeToken,
                actor.Provider,
                logger,
                cancellationToken));
            var details = await Task.WhenAll(tasks).ConfigureAwait(false);
            return details.Where(actor => actor is not null).Cast<RemoteActor>().ToList();
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or JsonException)
        {
            logger.ErrorException("Online actor lookup failed for " + record.Name, exception);
            return Array.Empty<RemoteActor>();
        }
    }

    private static IEnumerable<RemoteActorSearch> KnownActors(ActorRecord record, IReadOnlyCollection<string> sourceOrder)
    {
        var known = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var provider in sourceOrder)
        {
            if (record.ExternalIds.TryGetValue(provider, out var id) && !string.IsNullOrWhiteSpace(id)) known[provider] = id;
        }

        if (record.ExternalIds.TryGetValue("MetaTube", out var metaTube))
        {
            var separator = metaTube.IndexOf(':');
            if (separator > 0 && separator < metaTube.Length - 1)
                known.TryAdd(metaTube[..separator], metaTube[(separator + 1)..]);
        }

        return known.Select(pair => new RemoteActorSearch { Provider = pair.Key, Id = pair.Value, Name = record.Name });
    }

    private static bool Matches(RemoteActorSearch actor, IReadOnlyCollection<string> identities) =>
        identities.Any(identity => string.Equals(actor.Name, identity, StringComparison.OrdinalIgnoreCase)
                                   || actor.Aliases.Any(alias => string.Equals(alias, identity, StringComparison.OrdinalIgnoreCase)));

    private static RemoteActorSearch? SelectUnambiguous(IEnumerable<RemoteActorSearch> candidates, IReadOnlyCollection<string> identities)
    {
        var ranked = candidates.Select(actor => new
            {
                Actor = actor,
                Score = identities.Count(identity => string.Equals(actor.Name, identity, StringComparison.OrdinalIgnoreCase)
                                                     || actor.Aliases.Any(alias => string.Equals(alias, identity, StringComparison.OrdinalIgnoreCase)))
            })
            .OrderByDescending(candidate => candidate.Score)
            .ToList();
        if (ranked.Count == 0 || ranked[0].Score == 0) return null;
        if (ranked.Count > 1 && ranked[0].Score == ranked[1].Score) return null;
        return ranked[0].Actor;
    }

    private static int SourceRank(string provider, IReadOnlyList<string> sourceOrder)
    {
        var index = sourceOrder.ToList().FindIndex(value => string.Equals(value, provider, StringComparison.OrdinalIgnoreCase));
        return index < 0 ? int.MaxValue : index;
    }

    private static bool ContainsJapanese(string value) => value.Any(character =>
        character is >= '\u3040' and <= '\u30ff' or >= '\u3400' and <= '\u9fff');

    private static async Task<RemoteActor?> TryGetActor(string url, string token, string provider, ILogger logger, CancellationToken cancellationToken)
    {
        try
        {
            return (await Get<ApiEnvelope<RemoteActor>>(url, token, cancellationToken).ConfigureAwait(false)).Data;
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or JsonException)
        {
            logger.ErrorException("Actor source failed: " + provider, exception);
            return null;
        }
    }

    private static async Task<T> Get<T>(string url, string token, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, url);
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
        request.Headers.UserAgent.ParseAdd("JAV_CUSTOM_PROVIDER/1.0");
        if (!string.IsNullOrWhiteSpace(token)) request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        using var response = await HttpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();
        await using var stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        return (await JsonSerializer.DeserializeAsync<T>(stream, JsonOptions, cancellationToken).ConfigureAwait(false))!;
    }
}
