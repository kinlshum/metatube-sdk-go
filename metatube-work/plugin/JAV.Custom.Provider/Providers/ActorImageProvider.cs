using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Configuration;
using MediaBrowser.Model.Entities;
using MediaBrowser.Model.Logging;
using MediaBrowser.Model.Providers;

namespace JAV.Custom.Provider.Providers;

public sealed class ActorImageProvider : ProviderBase, IRemoteImageProvider, IHasOrder
{
    private readonly ILogger _logger;

    public ActorImageProvider(ILogManager logManager)
        : base(logManager.GetLogger(Plugin.ProviderName))
    {
        _logger = logManager.GetLogger(Plugin.ProviderName);
    }

    public string Name => Plugin.ProviderName;
    public int Order => 2000;

    public Task<IEnumerable<RemoteImageInfo>> GetImages(BaseItem item, LibraryOptions libraryOptions, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var id = item.GetProviderId(Plugin.ProviderId);
        if (string.IsNullOrWhiteSpace(id))
            return Task.FromResult(Enumerable.Empty<RemoteImageInfo>());

        var record = RecordStore.FindById(RecordStore.Load(_logger), id);
        return Task.FromResult(record?.ImageUrls.Select(url => new RemoteImageInfo
        {
            ProviderName = Name,
            Type = ImageType.Primary,
            Url = url
        }) ?? Enumerable.Empty<RemoteImageInfo>());
    }

    public bool Supports(BaseItem item) => item is Person;
    public IEnumerable<ImageType> GetSupportedImages(BaseItem item) => new[] { ImageType.Primary };
}
