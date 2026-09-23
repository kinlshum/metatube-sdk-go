using MediaBrowser.Controller.Plugins;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Logging;
using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Library;
using MediaBrowser.Model.Configuration;
using MediaBrowser.Model.Entities;
using MediaBrowser.Model.Events;

namespace JAV.Custom.Provider;

public sealed class ServerEntryPoint : IServerEntryPoint
{
    private readonly IProviderManager _providerManager;
    private readonly ILibraryManager _libraryManager;
    private readonly ILogger _logger;

    public ServerEntryPoint(IProviderManager providerManager, ILibraryManager libraryManager, ILogManager logManager)
    {
        _providerManager = providerManager;
        _libraryManager = libraryManager;
        _logger = logManager.GetLogger(Plugin.ProviderName);
    }

    public void Run()
    {
        var imageProviders = _providerManager.ImageProviders.Select(provider => provider.Name).Distinct().ToArray();
        _logger.Info("JAV_CUSTOM_PROVIDER started. Actor image provider registered: {0}",
            imageProviders.Contains(Plugin.ProviderName, StringComparer.OrdinalIgnoreCase));
        var metadataProviders = _providerManager.GetEnabledMetadataProviders(new Person(), new LibraryOptions())
            .Select(provider => provider.Name).Distinct().ToArray();
        _logger.Info("JAV_CUSTOM_PROVIDER actor metadata registered: {0}. Enabled person providers: {1}",
            metadataProviders.Contains(Plugin.ProviderName, StringComparer.OrdinalIgnoreCase),
            string.Join(", ", metadataProviders));
        _providerManager.RefreshCompleted += OnRefreshCompleted;
    }

    public void Dispose()
    {
        _providerManager.RefreshCompleted -= OnRefreshCompleted;
    }

    private void OnRefreshCompleted(object? sender, GenericEventArgs<RefreshProgressInfo> eventArgs)
    {
        if (eventArgs.Argument.Item is not Person person) return;
        var providerId = person.GetProviderId(Plugin.ProviderId);
        if (string.IsNullOrWhiteSpace(providerId) ||
            !ActorRenameCoordinator.TryTake(providerId, out var canonicalName) ||
            string.Equals(person.Name, canonicalName, StringComparison.Ordinal) &&
            string.Equals(person.SortName, canonicalName, StringComparison.Ordinal)) return;

        var previousName = person.Name;
        person.Name = canonicalName;
        person.SortName = canonicalName;
        _libraryManager.UpdateItem(person, person, ItemUpdateType.MetadataEdit);
        _logger.Info("JAV_CUSTOM_PROVIDER renamed person '{0}' to '{1}' after metadata refresh.",
            previousName, canonicalName);
    }
}
