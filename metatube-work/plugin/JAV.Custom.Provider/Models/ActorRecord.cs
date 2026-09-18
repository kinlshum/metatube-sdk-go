namespace JAV.Custom.Provider.Models;

public sealed class ActorRecord
{
    public string Id { get; set; } = string.Empty;
    public string Name { get; set; } = string.Empty;
    public List<string> Aliases { get; set; } = new();
    public string Birthday { get; set; } = string.Empty;
    public string DeathDate { get; set; } = string.Empty;
    public string DebutDate { get; set; } = string.Empty;
    public int? DebutAge { get; set; }
    public string DebutTitle { get; set; } = string.Empty;
    public int? EmbyMovieCount { get; set; }
    public int? JavDbMovieCount { get; set; }
    public string PlaceOfBirth { get; set; } = string.Empty;
    public string Nationality { get; set; } = string.Empty;
    public string Measurements { get; set; } = string.Empty;
    public string CupSize { get; set; } = string.Empty;
    public string AvActivity { get; set; } = string.Empty;
    public string Sign { get; set; } = string.Empty;
    public string BloodType { get; set; } = string.Empty;
    public string Height { get; set; } = string.Empty;
    public string Agency { get; set; } = string.Empty;
    public string Hobbies { get; set; } = string.Empty;
    public string AvAppearancePeriod { get; set; } = string.Empty;
    public string Overview { get; set; } = string.Empty;
    public List<string> Tags { get; set; } = new();
    public Dictionary<string, string> ExternalIds { get; set; } = new(StringComparer.OrdinalIgnoreCase);
    public Dictionary<string, string> Urls { get; set; } = new(StringComparer.OrdinalIgnoreCase);
    public List<string> ImageUrls { get; set; } = new();
}
