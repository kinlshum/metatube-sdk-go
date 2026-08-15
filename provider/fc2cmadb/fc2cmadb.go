package fc2cmadb

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/providerbridge"
)

const (
	Name     = "FC2CMADB"
	Priority = 997
)

func New() *providerbridge.Provider {
	return providerbridge.New(Name, "https://fc2cmadb.com/", Priority)
}

func init() { provider.Register(Name, New) }
