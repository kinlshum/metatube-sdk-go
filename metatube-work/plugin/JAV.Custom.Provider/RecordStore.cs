using System.Text.Json;
using JAV.Custom.Provider.Models;
using MediaBrowser.Model.Logging;

namespace JAV.Custom.Provider;

internal static class RecordStore
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        AllowTrailingCommas = true,
        ReadCommentHandling = JsonCommentHandling.Skip
    };

    public static IReadOnlyList<ActorRecord> Load(ILogger logger)
    {
        try
        {
            var json = Plugin.Instance.Configuration.RecordsJson;
            if (string.IsNullOrWhiteSpace(json)) json = Configuration.PluginConfiguration.DefaultRecords;
            return JsonSerializer.Deserialize<List<ActorRecord>>(
                       json, JsonOptions)?
                   .Where(IsValid)
                   .GroupBy(record => record.Id, StringComparer.OrdinalIgnoreCase)
                   .Select(group => group.First())
                   .ToList()
                   ?? new List<ActorRecord>();
        }
        catch (JsonException exception)
        {
            logger.ErrorException("Invalid JAV_CUSTOM_PROVIDER JSON", exception);
            return Array.Empty<ActorRecord>();
        }
    }

    public static ActorRecord? FindById(IEnumerable<ActorRecord> records, string? id) =>
        records.FirstOrDefault(record => string.Equals(record.Id, id, StringComparison.OrdinalIgnoreCase));

    public static IEnumerable<ActorRecord> Search(IEnumerable<ActorRecord> records, string? query)
    {
        var value = query?.Trim();
        if (string.IsNullOrWhiteSpace(value)) return Array.Empty<ActorRecord>();

        return records.Where(record =>
            string.Equals(record.Name, value, StringComparison.OrdinalIgnoreCase)
            || record.Name.Contains(value, StringComparison.OrdinalIgnoreCase)
            || record.Aliases.Any(alias => string.Equals(alias, value, StringComparison.OrdinalIgnoreCase)
                                           || alias.Contains(value, StringComparison.OrdinalIgnoreCase)));
    }

    private static bool IsValid(ActorRecord record) =>
        !string.IsNullOrWhiteSpace(record.Id) && !string.IsNullOrWhiteSpace(record.Name);
}
