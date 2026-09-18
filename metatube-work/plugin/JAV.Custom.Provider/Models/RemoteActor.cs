using System.Text.Json;
using System.Text.Json.Serialization;

namespace JAV.Custom.Provider.Models;

public class RemoteActorSearch
{
    [JsonPropertyName("id")] public string Id { get; set; } = string.Empty;
    [JsonPropertyName("name")] public string Name { get; set; } = string.Empty;
    [JsonPropertyName("provider")] public string Provider { get; set; } = string.Empty;
    [JsonPropertyName("homepage")] public string Homepage { get; set; } = string.Empty;
    [JsonPropertyName("aliases")] public string[] Aliases { get; set; } = Array.Empty<string>();
    [JsonPropertyName("images")] public string[] Images { get; set; } = Array.Empty<string>();
}

public sealed class RemoteActor : RemoteActorSearch
{
    [JsonPropertyName("birthday"), JsonConverter(typeof(LenientDateTimeConverter))]
    public DateTime Birthday { get; set; }

    [JsonPropertyName("debut_date"), JsonConverter(typeof(LenientDateTimeConverter))]
    public DateTime DebutDate { get; set; }
    [JsonPropertyName("debut_title")] public string DebutTitle { get; set; } = string.Empty;
    [JsonPropertyName("av_appearance_period")] public string AvAppearancePeriod { get; set; } = string.Empty;
    [JsonPropertyName("tags")] public string[] Tags { get; set; } = Array.Empty<string>();
    [JsonPropertyName("blood_type")] public string BloodType { get; set; } = string.Empty;
    [JsonPropertyName("cup_size")] public string CupSize { get; set; } = string.Empty;
    [JsonPropertyName("measurements")] public string Measurements { get; set; } = string.Empty;
    [JsonPropertyName("nationality")] public string Nationality { get; set; } = string.Empty;
    [JsonPropertyName("height")] public int Height { get; set; }
    [JsonPropertyName("hobby")] public string Hobby { get; set; } = string.Empty;
    [JsonPropertyName("skill")] public string Skill { get; set; } = string.Empty;
    [JsonPropertyName("summary")] public string Summary { get; set; } = string.Empty;
    [JsonPropertyName("place_of_birth")] public string PlaceOfBirth { get; set; } = string.Empty;
    [JsonPropertyName("original_name")] public string OriginalName { get; set; } = string.Empty;
    [JsonPropertyName("external_ids")] public Dictionary<string, string> ExternalIds { get; set; } = new(StringComparer.OrdinalIgnoreCase);
    [JsonPropertyName("urls")] public Dictionary<string, string> Urls { get; set; } = new(StringComparer.OrdinalIgnoreCase);
}

/// <summary>
/// Resolver and upstream actor sources use an empty string when an exact date is
/// unknown. Treat missing or malformed dates as DateTime.MinValue so one optional
/// field cannot discard an otherwise valid actor identity.
/// </summary>
internal sealed class LenientDateTimeConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType == JsonTokenType.Null) return DateTime.MinValue;
        if (reader.TokenType != JsonTokenType.String)
        {
            reader.Skip();
            return DateTime.MinValue;
        }

        var value = reader.GetString();
        return DateTime.TryParse(value, out var date) ? date : DateTime.MinValue;
    }

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
    {
        if (value.Year <= 1) writer.WriteStringValue(string.Empty);
        else writer.WriteStringValue(value.ToString("yyyy-MM-dd"));
    }
}

public sealed class ApiEnvelope<T>
{
    [JsonPropertyName("data")] public T? Data { get; set; }
}
