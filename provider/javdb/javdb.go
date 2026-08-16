package javdb

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/providerbridge"
)

const (
	Name     = "JavDB"
	Priority = 993
)

func New() *providerbridge.Provider {
	return providerbridge.NewGeneric(Name, "https://javdb.com/", Priority)
}

func init() { provider.Register(Name, New) }
