using System.Net.Http.Json;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Jellyfin.Plugin.MetaTube.Helpers;

/// <summary>
/// Resolves JavTrailers pages through the MetaTube project's own FlareSolverr.
/// This client is intentionally local to the plugin: it does not call JAV Master.
/// </summary>
public static class JavTrailersClient
{
    private static readonly HttpClient HttpClient = new() { Timeout = TimeSpan.FromSeconds(45) };

    public sealed record Trailer(string VideoUrl, string ImageUrl, string PageUrl);

    public static async Task<Trailer> GetTrailerAsync(string catalogCode, string solverUrl,
        CancellationToken cancellationToken)
    {
        var pageUrl = GetPageUrl(catalogCode);
        if (string.IsNullOrWhiteSpace(pageUrl) || string.IsNullOrWhiteSpace(solverUrl)) return null;

        using var request = new HttpRequestMessage(HttpMethod.Post, solverUrl)
        {
            Content = JsonContent.Create(new
            {
                cmd = "request.get",
                url = pageUrl,
                maxTimeout = 35_000
            })
        };
        using var response = await HttpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();
        using var document = await JsonDocument.ParseAsync(
            await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false),
            cancellationToken: cancellationToken).ConfigureAwait(false);

        var root = document.RootElement;
        if (!root.TryGetProperty("status", out var status) || status.GetString() != "ok")
            throw new InvalidOperationException("FlareSolverr could not open the JavTrailers page.");
        if (!root.TryGetProperty("solution", out var solution)) return null;
        var html = solution.TryGetProperty("response", out var page) ? page.GetString() : null;
        var finalUrl = solution.TryGetProperty("url", out var resolved) ? resolved.GetString() : pageUrl;
        if (string.IsNullOrWhiteSpace(html)) return null;

        // The page contains both the original DMM URL and JavTrailers' own
        // media mirror. DMM rejects server-side playback in many regions, so
        // prefer the mirror that Emby and Windmill can actually reach.
        var videoUrl = ExtractUrl(html, finalUrl!,
            """(?:https?:)?//media\.javtrailers\.com/[^"'<>\\\s]+?\.m3u8(?:\?[^"'<>\\\s]*)?""");
        if (string.IsNullOrWhiteSpace(videoUrl))
            videoUrl = ExtractUrl(html, finalUrl!, """(?:https?:)?//[^"'<>\\\s]+?\.m3u8(?:\?[^"'<>\\\s]*)?""");
        var imageUrl = ExtractMetaImage(html, finalUrl!);
        return string.IsNullOrWhiteSpace(videoUrl) ? null : new Trailer(videoUrl, imageUrl, finalUrl!);
    }

    private static string GetPageUrl(string catalogCode)
    {
        var match = Regex.Match(catalogCode?.Trim() ?? string.Empty, @"^([A-Za-z]+)[-_ ]*(\d+)$");
        if (!match.Success || !int.TryParse(match.Groups[2].Value, out var number)) return null;
        return $"https://javtrailers.com/video/{match.Groups[1].Value.ToLowerInvariant()}{number:D5}";
    }

    private static string ExtractUrl(string html, string baseUrl, string pattern)
    {
        var match = Regex.Match(html, pattern, RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        if (!match.Success) return string.Empty;
        var value = match.Value.Replace("\\/", "/");
        return value.StartsWith("//", StringComparison.Ordinal) ? "https:" + value : value;
    }

    private static string ExtractMetaImage(string html, string baseUrl)
    {
        var match = Regex.Match(html,
            """<meta[^>]+(?:property|name)=["'](?:og:image|twitter:image)["'][^>]+content=["'](?<url>[^"']+)""",
            RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        if (!match.Success)
            match = Regex.Match(html,
                """<meta[^>]+content=["'](?<url>[^"']+)["'][^>]+(?:property|name)=["'](?:og:image|twitter:image)["']""",
                RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        if (!match.Success) return string.Empty;
        var raw = match.Groups["url"].Value.Replace("&amp;", "&").Replace("\\/", "/");
        return Uri.TryCreate(new Uri(baseUrl), raw, out var uri) ? uri.AbsoluteUri : string.Empty;
    }
}
