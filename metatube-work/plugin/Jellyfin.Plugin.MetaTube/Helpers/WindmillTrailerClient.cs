using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;

namespace Jellyfin.Plugin.MetaTube.Helpers;

/// <summary>Submits a real, asynchronous Windmill job for one resolved movie trailer.</summary>
public static class WindmillTrailerClient
{
    private static readonly HttpClient HttpClient = new() { Timeout = TimeSpan.FromSeconds(20) };

    public static async Task<string> SubmitAsync(string baseUrl, string workspace, string scriptPath,
        string token, string code, string trailerUrl, string imageUrl, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(baseUrl) || string.IsNullOrWhiteSpace(workspace) ||
            string.IsNullOrWhiteSpace(scriptPath) || string.IsNullOrWhiteSpace(token))
            throw new InvalidOperationException("Windmill trailer job configuration is incomplete.");
        if (scriptPath.Contains("..", StringComparison.Ordinal))
            throw new InvalidOperationException("Windmill trailer script path is invalid.");

        var url = $"{baseUrl.TrimEnd('/')}/api/w/{Uri.EscapeDataString(workspace)}/jobs/run/p/{scriptPath.TrimStart('/')}";
        using var request = new HttpRequestMessage(HttpMethod.Post, url)
        {
            Content = JsonContent.Create(new
            {
                code,
                trailer_url = trailerUrl,
                image_url = imageUrl ?? string.Empty
            })
        };
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token.Trim());
        using var response = await HttpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();
        var body = (await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false)).Trim();
        if (body.StartsWith('"')) body = JsonSerializer.Deserialize<string>(body) ?? body;
        if (string.IsNullOrWhiteSpace(body)) throw new InvalidOperationException("Windmill returned no job id.");
        return body;
    }
}
