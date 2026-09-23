using Jellyfin.Plugin.MetaTube.Extensions;
using Jellyfin.Plugin.MetaTube.Helpers;
using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Entities.Movies;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Entities;
using MediaBrowser.Model.Providers;
#if __EMBY__
using MediaBrowser.Model.Configuration;
using MediaBrowser.Model.Logging;

#else
using Microsoft.Extensions.Logging;
#endif

namespace Jellyfin.Plugin.MetaTube.Providers;

public class MovieImageProvider : BaseProvider, IRemoteImageProvider, IHasOrder
{
#if __EMBY__
    public MovieImageProvider(ILogManager logManager) : base(logManager.CreateLogger<MovieImageProvider>())
#else
    public MovieImageProvider(ILogger<MovieImageProvider> logger) : base(logger)
#endif
    {
    }

#if __EMBY__
    public async Task<IEnumerable<RemoteImageInfo>> GetImages(BaseItem item, LibraryOptions libraryOptions,
        CancellationToken cancellationToken)
#else
    public async Task<IEnumerable<RemoteImageInfo>> GetImages(BaseItem item, CancellationToken cancellationToken)
#endif
    {
        var pid = item.GetPid(Plugin.ProviderId);
        if (string.IsNullOrWhiteSpace(pid.Id) || string.IsNullOrWhiteSpace(pid.Provider))
            return Enumerable.Empty<RemoteImageInfo>();

        var m = await ApiClient.GetMovieInfoAsync(pid.Provider, pid.Id, cancellationToken);
        var images = new List<RemoteImageInfo>
        {
            new()
            {
                ProviderName = Name,
                Type = ImageType.Primary,
                Url = ApiClient.GetPrimaryImageApiUrl(m.Provider, m.Id, pid.Position ?? -1)
            },
            new()
            {
                ProviderName = Name,
                Type = ImageType.Thumb,
                Url = ApiClient.GetThumbImageApiUrl(m.Provider, m.Id)
            },
            new()
            {
                ProviderName = Name,
                Type = ImageType.Backdrop,
                Url = ApiClient.GetBackdropImageApiUrl(m.Provider, m.Id)
            }
        };

        foreach (var imageUrl in m.PreviewImages ?? Enumerable.Empty<string>())
        {
            images.Add(new RemoteImageInfo
            {
                ProviderName = Name,
                Type = ImageType.Primary,
                Url = ApiClient.GetPrimaryImageApiUrl(m.Provider, m.Id, imageUrl, pid.Position ?? -1)
            });

            images.Add(new RemoteImageInfo
            {
                ProviderName = Name,
                Type = ImageType.Thumb,
                Url = ApiClient.GetThumbImageApiUrl(m.Provider, m.Id, imageUrl)
            });

            images.Add(new RemoteImageInfo
            {
                ProviderName = Name,
                Type = ImageType.Backdrop,
                Url = ApiClient.GetBackdropImageApiUrl(m.Provider, m.Id, imageUrl)
            });
        }

#if __EMBY__
        if (Configuration.SaveAllBackdropsLocally && libraryOptions.SaveLocalMetadata
                                                  && MetadataRefreshTracker.Consume(m.Provider, m.Id))
            await SaveAllBackdropsLocally(item, m, cancellationToken).ConfigureAwait(false);
#endif

        return images;
    }

#if __EMBY__
    private async Task SaveAllBackdropsLocally(BaseItem item, Metadata.MovieInfo movie,
        CancellationToken cancellationToken)
    {
        var mediaDirectory = Path.GetDirectoryName(item.Path);
        if (string.IsNullOrWhiteSpace(mediaDirectory) || !Directory.Exists(mediaDirectory)) return;

        var urls = new List<string> { ApiClient.GetBackdropImageApiUrl(movie.Provider, movie.Id) };
        urls.AddRange((movie.PreviewImages ?? Array.Empty<string>())
            .Select(url => ApiClient.GetBackdropImageApiUrl(movie.Provider, movie.Id, url)));

        try
        {
            for (var index = 0; index < urls.Count; index++)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var fileName = index == 0 ? "fanart.jpg" : $"fanart{index}.jpg";
                var destination = Path.Combine(mediaDirectory, fileName);
                var temporary = destination + ".metatube.tmp";
                var response = await ApiClient.GetImageResponse(urls[index], cancellationToken).ConfigureAwait(false);
                await using (response.Content.ConfigureAwait(false))
                await using (var output = new FileStream(temporary, FileMode.Create, FileAccess.Write, FileShare.None,
                                 81920, true))
                    await response.Content.CopyToAsync(output, cancellationToken).ConfigureAwait(false);
                File.Move(temporary, destination, true);
            }

            foreach (var path in Directory.EnumerateFiles(mediaDirectory, "fanart*.jpg"))
            {
                var stem = Path.GetFileNameWithoutExtension(path);
                if (stem == "fanart") continue;
                if (int.TryParse(stem["fanart".Length..], out var index) && index >= urls.Count)
                    File.Delete(path);
            }

            Logger.Info("Saved {0} backdrops after metadata refresh for {1}", urls.Count, item.Path);
        }
        catch (Exception exception) when (exception is not OperationCanceledException)
        {
            Logger.ErrorException("Failed to save all backdrops beside " + item.Path, exception);
        }
    }
#endif

    public bool Supports(BaseItem item)
    {
        return item is Movie;
    }

    public IEnumerable<ImageType> GetSupportedImages(BaseItem item)
    {
        return new List<ImageType>
        {
            ImageType.Primary,
            ImageType.Thumb,
            ImageType.Backdrop
        };
    }
}
