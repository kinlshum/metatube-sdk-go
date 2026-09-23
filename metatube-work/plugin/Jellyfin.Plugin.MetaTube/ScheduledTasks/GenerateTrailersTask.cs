using System.Diagnostics;
using System.Text;
using Jellyfin.Plugin.MetaTube.Extensions;
using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Library;
using MediaBrowser.Model.Entities;
using MediaBrowser.Model.Tasks;
#if __EMBY__
using MediaBrowser.Controller.Entities.Movies;
using MediaBrowser.Model.Logging;

#else
using Microsoft.Extensions.Logging;
using Jellyfin.Data.Enums;
#endif

namespace Jellyfin.Plugin.MetaTube.ScheduledTasks;

public class GenerateTrailersTask : IScheduledTask
{
    // Emby: trailers can be stored in a trailers sub-folder.
    // https://support.emby.media/support/solutions/articles/44001159193-trailers
    private const string TrailersFolder = "trailers";

    // Uniform suffix for all trailer files.
    private const string TrailerFileStemSuffix = "-Trailer";
    private const string TrailerFileSuffix = TrailerFileStemSuffix + ".strm";
    private const string TrailerVideoSuffix = TrailerFileStemSuffix + ".mp4";
    private const string TrailerSearchPattern = $"*{TrailerFileStemSuffix}.*";

    // UTF-8 without BOM encoding.
    private static readonly Encoding Utf8WithoutBom = new UTF8Encoding(false);
    private static readonly HttpClient TrailerImageClient = new() { Timeout = TimeSpan.FromSeconds(30) };

    private readonly ILibraryManager _libraryManager;
    private readonly ILogger _logger;

#if __EMBY__
    public GenerateTrailersTask(ILogManager logManager, ILibraryManager libraryManager)
    {
        _logger = logManager.CreateLogger<GenerateTrailersTask>();
        _libraryManager = libraryManager;
    }
#else
    public GenerateTrailersTask(ILogger<GenerateTrailersTask> logger, ILibraryManager libraryManager)
    {
        _logger = logger;
        _libraryManager = libraryManager;
    }
#endif

    public string Key => $"{Plugin.ProviderName}GenerateTrailers";

    public string Name => "Generate Trailers";

    public string Description => $"Generates video trailers provided by {Plugin.ProviderName} in library.";

    public string Category => Plugin.ProviderName;

    public IEnumerable<TaskTriggerInfo> GetDefaultTriggers()
    {
        yield return new TaskTriggerInfo
        {
#if __EMBY__
            Type = TaskTriggerInfo.TriggerDaily,
#else
            Type = TaskTriggerInfoType.DailyTrigger,
#endif
            TimeOfDayTicks = TimeSpan.FromHours(1).Ticks
        };
    }

#if __EMBY__
    public async Task Execute(CancellationToken cancellationToken, IProgress<double> progress)
#else
    public async Task ExecuteAsync(IProgress<double> progress, CancellationToken cancellationToken)
#endif
    {
        // Stop the task if disabled.
        if (!Plugin.Instance.Configuration.EnableTrailers)
            return;

        await Task.Yield();

        progress?.Report(0);

        var items = _libraryManager.GetItemList(new InternalItemsQuery
        {
            MediaTypes = new[] { MediaType.Video },
#if __EMBY__
            HasAnyProviderId = new[] { Plugin.ProviderId },
            IncludeItemTypes = new[] { nameof(Movie) },
#else
            HasAnyProviderId = new Dictionary<string, string> { { Plugin.ProviderId, string.Empty } },
            IncludeItemTypes = new[] { BaseItemKind.Movie }
#endif
        }).ToList();

        foreach (var (idx, item) in items.WithIndex())
        {
            cancellationToken.ThrowIfCancellationRequested();
            progress?.Report((double)idx / items.Count * 100);

            try
            {
                var trailersFolderPath = Path.Join(item.ContainingFolderPath, TrailersFolder);

                // Skip if contains .ignore file.
                if (File.Exists(Path.Join(trailersFolderPath, ".ignore")))
                    continue;

                var trailerUrl = item.GetTrailerUrl();

                // Skip if no remote trailers.
                if (string.IsNullOrWhiteSpace(trailerUrl))
                {
                    if (Directory.Exists(trailersFolderPath))
                    {
                        // Delete obsolete trailer files.
                        DeleteFiles(trailersFolderPath, TrailerSearchPattern);

                        // Delete directory if empty.
                        DeleteDirectoryIfEmpty(trailersFolderPath);
                    }

                    continue;
                }

                var trailerStem = Path.Join(trailersFolderPath, $"{item.Name.Split().First()}{TrailerFileStemSuffix}");
                var localTrailerPath = trailerStem + ".mp4";
                var streamTrailerPath = trailerStem + ".strm";

#if __EMBY__
                var lastSavedUtcDateTime = item.DateLastSaved.UtcDateTime;
#else
                var lastSavedUtcDateTime = item.DateLastSaved.ToUniversalTime();
#endif

                // Create trailers folder if not exists.
                if (!Directory.Exists(trailersFolderPath))
                    Directory.CreateDirectory(trailersFolderPath);

                var localTrailerReady = Plugin.Instance.Configuration.DownloadTrailersLocally &&
                                        File.Exists(localTrailerPath) &&
                                        File.GetLastWriteTimeUtc(localTrailerPath) >= lastSavedUtcDateTime;
                if (!localTrailerReady && Plugin.Instance.Configuration.DownloadTrailersLocally)
                    localTrailerReady = await DownloadTrailerAsync(item, trailerUrl, localTrailerPath, cancellationToken)
                        .ConfigureAwait(false);

                if (localTrailerReady)
                {
                    // A real local trailer takes precedence over the older remote .strm path.
                    if (File.Exists(streamTrailerPath)) File.Delete(streamTrailerPath);
                    DeleteFiles(trailersFolderPath, TrailerSearchPattern, localTrailerPath, Path.ChangeExtension(localTrailerPath, ".jpg"));
                    await SaveTrailerImageAsync(item, localTrailerPath, cancellationToken).ConfigureAwait(false);
                    continue;
                }

                // Keep the previous streaming behavior if local download is disabled or fails.
                if (File.Exists(streamTrailerPath) &&
                    string.Equals(await File.ReadAllTextAsync(streamTrailerPath, cancellationToken), trailerUrl))
                {
                    File.SetLastWriteTimeUtc(streamTrailerPath, DateTime.UtcNow);
                    await SaveTrailerImageAsync(item, streamTrailerPath, cancellationToken).ConfigureAwait(false);
                    continue;
                }

                DeleteFiles(trailersFolderPath, TrailerSearchPattern, streamTrailerPath, Path.ChangeExtension(streamTrailerPath, ".jpg"));
                _logger.Info("Create streaming trailer for video {0} at {1}", item.Name, streamTrailerPath);
                await File.WriteAllTextAsync(streamTrailerPath, trailerUrl, Utf8WithoutBom, cancellationToken);
                await SaveTrailerImageAsync(item, streamTrailerPath, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception e)
            {
                _logger.Error("Generate trailer for video {0} error: {1}", item.Name, e.Message);
            }
        }

        progress?.Report(100);
    }

    private async Task<bool> DownloadTrailerAsync(BaseItem item, string trailerUrl, string destination,
        CancellationToken cancellationToken)
    {
        var temporary = destination + ".metatube.tmp";
        try
        {
            var start = new ProcessStartInfo("/bin/ffmpeg")
            {
                RedirectStandardError = true,
                RedirectStandardOutput = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };
            start.ArgumentList.Add("-nostdin");
            start.ArgumentList.Add("-y");
            start.ArgumentList.Add("-i");
            start.ArgumentList.Add(trailerUrl);
            start.ArgumentList.Add("-c");
            start.ArgumentList.Add("copy");
            start.ArgumentList.Add("-movflags");
            start.ArgumentList.Add("+faststart");
            start.ArgumentList.Add(temporary);
            using var process = Process.Start(start) ?? throw new InvalidOperationException("Unable to start ffmpeg");
            var error = await process.StandardError.ReadToEndAsync(cancellationToken).ConfigureAwait(false);
            await process.WaitForExitAsync(cancellationToken).ConfigureAwait(false);
            if (process.ExitCode != 0 || !File.Exists(temporary) || new FileInfo(temporary).Length == 0)
                throw new InvalidOperationException($"ffmpeg exited {process.ExitCode}: {error[^Math.Min(error.Length, 500)..]}");
            File.Move(temporary, destination, true);
            _logger.Info("Downloaded local trailer for video {0} at {1}", item.Name, destination);
            return true;
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            if (File.Exists(temporary)) File.Delete(temporary);
            _logger.Warn("Download trailer for video {0} failed; using streaming fallback: {1}", item.Name, e.Message);
            return false;
        }
    }

    private async Task SaveTrailerImageAsync(BaseItem item, string trailerFilePath,
        CancellationToken cancellationToken)
    {
        var imageUrl = item.GetTrailerImageUrl();
        if (string.IsNullOrWhiteSpace(imageUrl)) return;

        var imagePath = Path.ChangeExtension(trailerFilePath, ".jpg");
        if (File.Exists(imagePath) && File.GetLastWriteTimeUtc(imagePath) >= item.DateLastSaved.UtcDateTime) return;

        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, imageUrl);
            request.Headers.UserAgent.ParseAdd("MetaTube/1.0");
            using var response = await TrailerImageClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
            response.EnsureSuccessStatusCode();
            var temporary = imagePath + ".metatube.tmp";
            await using (var input = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false))
            await using (var output = new FileStream(temporary, FileMode.Create, FileAccess.Write, FileShare.None,
                             81920, true))
                await input.CopyToAsync(output, cancellationToken).ConfigureAwait(false);
            File.Move(temporary, imagePath, true);
            _logger.Info("Saved trailer image for video {0} at {1}", item.Name, imagePath);
        }
        catch (Exception e) when (e is HttpRequestException or TaskCanceledException or IOException)
        {
            _logger.Warn("Save trailer image for video {0} failed: {1}", item.Name, e.Message);
        }
    }

    private static void DeleteFiles(string path, string searchPattern, params string[] excludedFiles)
    {
        DeleteFiles(Directory.GetFiles(path, searchPattern).Where(file => !excludedFiles.Contains(file)));
    }

    private static void DeleteFiles(IEnumerable<string> files)
    {
        foreach (var file in files) File.Delete(file);
    }

    private static void DeleteDirectoryIfEmpty(string path)
    {
        if (!Directory.GetDirectories(path).Any() && !Directory.GetFiles(path).Any())
            Directory.Delete(path);
    }
}