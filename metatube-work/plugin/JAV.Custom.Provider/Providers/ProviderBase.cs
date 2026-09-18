using MediaBrowser.Common.Net;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Logging;

namespace JAV.Custom.Provider.Providers;

public abstract class ProviderBase : IHasSupportedExternalIdentifiers
{
    private static readonly HttpClient HttpClient = new();
    private readonly ILogger _logger;

    protected ProviderBase(ILogger logger)
    {
        _logger = logger;
    }

    public string[] GetSupportedExternalIdentifiers() => new[] { Plugin.ProviderId };

    public async Task<HttpResponseInfo> GetImageResponse(string url, CancellationToken cancellationToken)
    {
        _logger.Debug("Downloading custom actor image: {0}", url);
        using var request = new HttpRequestMessage(HttpMethod.Get, url);
        request.Headers.UserAgent.ParseAdd("JAV_CUSTOM_PROVIDER/1.0");
        var response = await HttpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        return new HttpResponseInfo
        {
            Content = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false),
            ContentLength = response.Content.Headers.ContentLength,
            ContentType = response.Content.Headers.ContentType?.ToString(),
            StatusCode = response.StatusCode,
            Headers = response.Content.Headers.ToDictionary(pair => pair.Key, pair => string.Join(", ", pair.Value))
        };
    }
}
