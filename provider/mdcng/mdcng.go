package mdcng

import (
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/providerbridge"
)

const (
	Name     = "MDC-NG"
	Priority = 996
)

func New() *providerbridge.Provider {
	return providerbridge.New(Name, "http://192.168.10.170:9208/", Priority)
}

func init() { provider.Register(Name, New) }
