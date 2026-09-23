using MediaBrowser.Controller.Entities;
using MediaBrowser.Controller.Providers;
using MediaBrowser.Model.Entities;

namespace JAV.Custom.Provider.ExternalIds;

public abstract class PersonExternalId : IExternalId
{
    protected PersonExternalId(string providerName, string key, string urlFormatString = "")
    {
        ProviderName = providerName;
        Key = key;
        UrlFormatString = urlFormatString;
    }

    public string Name => ProviderName;
    public string ProviderName { get; }
    public string Key { get; }
    public string UrlFormatString { get; }
    public bool Supports(IHasProviderIds item) => item is Person;
}

public sealed class CustomProviderId() : PersonExternalId(Plugin.ProviderName, Plugin.ProviderId);
public sealed class AvLeagueId() : PersonExternalId("AV-LEAGUE", "AV-LEAGUE", "https://www.av-league.com/actress/{0}.html");
public sealed class XsListId() : PersonExternalId("XsList", "XsList", "https://xslist.org/en/model/{0}.html");
public sealed class MinnanoAvId() : PersonExternalId("Minnano-AV", "Minnano-AV", "https://www.minnano-av.com/actress{0}.html");
public sealed class GfriendsId() : PersonExternalId("Gfriends", "Gfriends", "https://github.com/gfriends/gfriends?gfriends-id={0}");
public sealed class JavLibraryIdolId() : PersonExternalId("JavLibrary Idol", "JavLibraryIdol", "https://www.javlibrary.com/en/vl_star.php?s={0}");
public sealed class JapanHdvIdolId() : PersonExternalId("JapanHDV Idol", "JapanHDVIdol");
public sealed class SextbActressId() : PersonExternalId("Sextb JAV Actress ID", "SextbActress", "https://sextb.net/actress/{0}");
public sealed class JavDbId() : PersonExternalId("Javdb", "JavdbActor", "https://javdb.com/actors/{0}.html");
public sealed class SupjavId() : PersonExternalId("Superjav", "SupjavActor", "https://supjav.com/category/cast/{0}");
public sealed class PubjavId() : PersonExternalId("Pubjav", "PubjavActor", "https://pubjav.com/tag/{0}/");
public sealed class SexctId() : PersonExternalId("Sexct", "SexctActor");
public sealed class BabepediaId() : PersonExternalId("Babepedia", "Babepedia", "https://www.babepedia.com/babe/{0}");
public sealed class JavDatabaseId() : PersonExternalId("JAVDatabase", "JAVDatabase", "https://www.javdatabase.com/idols/{0}/");
public sealed class OfficialWebsiteId() : PersonExternalId("Official website", "OfficialWebsite");
public sealed class InstagramId() : PersonExternalId("Instagram", "InstagramActor", "https://instagram.com/{0}/");
public sealed class TwitterId() : PersonExternalId("X (Twitter)", "TwitterActor", "https://twitter.com/{0}/");
public sealed class AlsoKnownAsId() : PersonExternalId("Also known as", "JAVAlsoKnownAs");
public sealed class DebutDateId() : PersonExternalId("Debut date", "JAVDebutDate");
public sealed class DebutTitleId() : PersonExternalId("Debut title", "JAVDebutTitle");
public sealed class MeasurementsId() : PersonExternalId("Measurements", "JAVMeasurements");
public sealed class CupSizeId() : PersonExternalId("Cup Size", "JAVCupSize");
public sealed class AvActivityId() : PersonExternalId("AV Activity", "JAVActivity");
public sealed class SignId() : PersonExternalId("Sign", "JAVSign");
public sealed class BloodTypeId() : PersonExternalId("Blood Type", "JAVBloodType");
public sealed class HeightId() : PersonExternalId("Height", "JAVHeight");
public sealed class AgencyId() : PersonExternalId("Agency", "JAVAgency");
public sealed class HobbiesId() : PersonExternalId("Hobbies and special skills", "JAVHobbies");
public sealed class AppearancePeriodId() : PersonExternalId("AV appearance period", "JAVAppearancePeriod");
public sealed class EmbyMovieCountId() : PersonExternalId("Emby movie count", "JAVEmbyMovieCount");
public sealed class JavDbMovieCountId() : PersonExternalId("JavDB movie count", "JAVJavDbMovieCount");
public sealed class JavDbMovieCountMaxId() : PersonExternalId("JavDB movie count max", "JAVJavDbMovieCountMax");
public sealed class JavDbMovieCountUpdatedId() : PersonExternalId("JavDB movie count updated", "JAVJavDbMovieCountUpdated");
