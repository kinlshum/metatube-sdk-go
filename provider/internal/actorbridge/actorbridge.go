package actorbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.ActorProvider = (*Provider)(nil)
	_ provider.ActorSearcher = (*Provider)(nil)
)

type Provider struct {
	*scraper.Scraper
	bridgeURL string
	client    *http.Client
}

func New(name, homepage string, priority float64) *Provider {
	bridgeURL := os.Getenv("METATUBE_PROVIDER_BRIDGE_URL")
	if bridgeURL == "" {
		bridgeURL = "http://metatube-provider-bridge:9210"
	}
	return &Provider{
		Scraper:   scraper.NewDefaultScraper(name, homepage, priority, language.Japanese),
		bridgeURL: strings.TrimRight(bridgeURL, "/"),
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *Provider) NormalizeActorID(id string) string { return strings.TrimSpace(id) }

func (p *Provider) ParseActorIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, ".html"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "", provider.ErrInvalidURL
	}
	return parts[len(parts)-1], nil
}

func (p *Provider) GetActorInfoByURL(rawURL string) (*model.ActorInfo, error) {
	id, err := p.ParseActorIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return p.GetActorInfoByID(id)
}

func (p *Provider) GetActorInfoByID(id string) (*model.ActorInfo, error) {
	endpoint := fmt.Sprintf("%s/v1/providers/%s/actors/%s", p.bridgeURL,
		url.PathEscape(p.Name()), url.PathEscape(p.NormalizeActorID(id)))
	info := &model.ActorInfo{}
	if err := p.get(endpoint, info); err != nil {
		return nil, err
	}
	info.Provider = p.Name()
	return info, nil
}

func (p *Provider) SearchActor(keyword string) ([]*model.ActorSearchResult, error) {
	endpoint := fmt.Sprintf("%s/v1/providers/%s/actors?q=%s", p.bridgeURL,
		url.PathEscape(p.Name()), url.QueryEscape(strings.TrimSpace(keyword)))
	results := []*model.ActorSearchResult{}
	if err := p.get(endpoint, &results); err != nil {
		return nil, err
	}
	for _, result := range results {
		result.Provider = p.Name()
	}
	return results, nil
}

func (p *Provider) get(endpoint string, target any) error {
	resp, err := p.client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider bridge returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (p *Provider) SetRequestTimeout(timeout time.Duration) { p.client.Timeout = timeout }
