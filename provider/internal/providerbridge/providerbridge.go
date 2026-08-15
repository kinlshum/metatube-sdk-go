package providerbridge

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
	"github.com/metatube-community/metatube-sdk-go/provider/fc2/fc2util"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*Provider)(nil)
	_ provider.MovieSearcher = (*Provider)(nil)
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

func (p *Provider) NormalizeMovieID(id string) string { return fc2util.ParseNumber(id) }

func (p *Provider) NormalizeMovieKeyword(keyword string) string { return fc2util.ParseNumber(keyword) }

func (p *Provider) ParseMovieIDFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	id := fc2util.ParseNumber(u.Path)
	if id == "" {
		return "", provider.ErrInvalidURL
	}
	return id, nil
}

func (p *Provider) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	id, err := p.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	return p.GetMovieInfoByID(id)
}

func (p *Provider) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	id = p.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	endpoint := fmt.Sprintf("%s/v1/providers/%s/movies/%s", p.bridgeURL,
		url.PathEscape(p.Name()), url.PathEscape(id))
	resp, err := p.client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider bridge returned %s", resp.Status)
	}
	info := &model.MovieInfo{}
	if err := json.NewDecoder(resp.Body).Decode(info); err != nil {
		return nil, err
	}
	info.Provider = p.Name()
	return info, nil
}

func (p *Provider) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	info, err := p.GetMovieInfoByID(keyword)
	if err != nil {
		return nil, err
	}
	return []*model.MovieSearchResult{info.ToSearchResult()}, nil
}

func (p *Provider) SetRequestTimeout(timeout time.Duration) {
	p.client.Timeout = timeout
}
