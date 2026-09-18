using System.Collections.Concurrent;

namespace Jellyfin.Plugin.MetaTube.Helpers;

public sealed class ActorCreationLock : IAsyncDisposable
{
    private static readonly ConcurrentDictionary<string, SemaphoreSlim> Locks =
        new(StringComparer.OrdinalIgnoreCase);

    private readonly IReadOnlyList<SemaphoreSlim> _acquiredLocks;

    private ActorCreationLock(IReadOnlyList<SemaphoreSlim> acquiredLocks)
    {
        _acquiredLocks = acquiredLocks;
    }

    public static async Task<ActorCreationLock> AcquireAsync(IEnumerable<string> actorNames,
        CancellationToken cancellationToken)
    {
        var locks = actorNames
            .Where(name => !string.IsNullOrWhiteSpace(name))
            .Select(NormalizeKey)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderBy(name => name, StringComparer.OrdinalIgnoreCase)
            .Select(name => Locks.GetOrAdd(name, _ => new SemaphoreSlim(1, 1)))
            .ToArray();

        var acquired = new List<SemaphoreSlim>(locks.Length);
        try
        {
            foreach (var actorLock in locks)
            {
                await actorLock.WaitAsync(cancellationToken).ConfigureAwait(false);
                acquired.Add(actorLock);
            }

            return new ActorCreationLock(acquired);
        }
        catch
        {
            Release(acquired);
            throw;
        }
    }

    public ValueTask DisposeAsync()
    {
        _ = ReleaseAfterSaveAsync(_acquiredLocks);
        return ValueTask.CompletedTask;
    }

    private static async Task ReleaseAfterSaveAsync(IReadOnlyList<SemaphoreSlim> acquiredLocks)
    {
        await Task.Delay(TimeSpan.FromSeconds(2)).ConfigureAwait(false);
        Release(acquiredLocks);
    }

    private static void Release(IEnumerable<SemaphoreSlim> acquiredLocks)
    {
        foreach (var actorLock in acquiredLocks.Reverse())
            actorLock.Release();
    }

    private static string NormalizeKey(string name)
    {
        return string.Join(' ', name.Normalize().Split((char[])null,
            StringSplitOptions.RemoveEmptyEntries));
    }
}
