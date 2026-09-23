using System.Collections.Concurrent;

namespace Jellyfin.Plugin.MetaTube.Helpers;

internal static class MetadataRefreshTracker
{
    private static readonly ConcurrentDictionary<string, DateTime> Pending =
        new(StringComparer.OrdinalIgnoreCase);

    public static void Mark(string provider, string id)
    {
        if (!string.IsNullOrWhiteSpace(provider) && !string.IsNullOrWhiteSpace(id))
            Pending[Key(provider, id)] = DateTime.UtcNow.AddMinutes(10);
    }

    public static bool Consume(string provider, string id)
    {
        var key = Key(provider, id);
        return Pending.TryRemove(key, out var expiresAt) && expiresAt >= DateTime.UtcNow;
    }

    private static string Key(string provider, string id) => $"{provider}:{id}";
}
