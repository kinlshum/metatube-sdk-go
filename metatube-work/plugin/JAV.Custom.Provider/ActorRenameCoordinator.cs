using System.Collections.Concurrent;

namespace JAV.Custom.Provider;

internal static class ActorRenameCoordinator
{
    private static readonly ConcurrentDictionary<string, string> Pending =
        new(StringComparer.OrdinalIgnoreCase);

    public static void Register(string providerId, string canonicalName)
    {
        if (!string.IsNullOrWhiteSpace(providerId) && !string.IsNullOrWhiteSpace(canonicalName))
            Pending[providerId] = canonicalName.Trim();
    }

    public static bool TryTake(string providerId, out string canonicalName) =>
        Pending.TryRemove(providerId, out canonicalName!);
}
