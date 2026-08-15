package fc2hub

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/providerbridge"
)

const (
	Name     = "fc2hub"
	Priority = 1000 - 1
)

func New() *providerbridge.Provider {
	return providerbridge.New(Name, "https://javten.com/", Priority)
}

func init() {
	provider.Register(Name, New)
}
