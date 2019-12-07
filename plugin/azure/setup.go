package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"

	publicAzureDNS "github.com/Azure/azure-sdk-for-go/profiles/latest/dns/mgmt/dns"
	privateAzureDNS "github.com/Azure/azure-sdk-for-go/profiles/latest/privatedns/mgmt/privatedns"

	azurerest "github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/caddyserver/caddy"
)

var log = clog.NewWithPlugin("azure")

func init() { plugin.Register("azure", setup) }

func setup(c *caddy.Controller) error {
	env, keys, fall, err := parse(c)
	if err != nil {
		return plugin.Error("azure", err)
	}
	ctx := context.Background()

	publicDNSClient := publicAzureDNS.NewRecordSetsClient(env.Values[auth.SubscriptionID])
	if publicDNSClient.Authorizer, err = env.GetAuthorizer(); err != nil {
		return plugin.Error("azure", err)
	}

	privateDNSClient := privateAzureDNS.NewRecordSetsClient(env.Values[auth.SubscriptionID])
	if privateDNSClient.Authorizer, err = env.GetAuthorizer(); err != nil {
		return plugin.Error("azure", err)
	}

	h, err := New(ctx, publicDNSClient, privateDNSClient, keys)
	if err != nil {
		return plugin.Error("azure", err)
	}
	h.Fall = fall
	if err := h.Run(ctx); err != nil {
		return plugin.Error("azure", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		h.Next = next
		return h
	})
	return nil
}

func parse(c *caddy.Controller) (auth.EnvironmentSettings, map[string][]string, fall.F, error) {
	resourceGroupMapping := map[string][]string{}
	resourceGroupSet := map[string]struct{}{}
	azureEnv := azurerest.PublicCloud
	env := auth.EnvironmentSettings{Values: map[string]string{}}

	var fall fall.F

	for c.Next() {
		args := c.RemainingArgs()

		for i := 0; i < len(args); i++ {
			parts := strings.SplitN(args[i], ":", 3)
			if len(parts) != 3 {
				return env, resourceGroupMapping, fall, c.Errf("invalid resource group/zone: %q", args[i])
			}
			resourceGroup, zoneName, access := parts[0], parts[1], parts[2]
			if resourceGroup == "" || zoneName == "" || (access != "public" && access != "private") {
				return env, resourceGroupMapping, fall, c.Errf("invalid resource group/zone/access: %q", args[i])
			}
			if _, ok := resourceGroupSet[resourceGroup+zoneName]; ok {
				return env, resourceGroupMapping, fall, c.Errf("conflicting zone: %q", args[i])
			}

			resourceGroupSet[resourceGroup+zoneName] = struct{}{}
			resourceGroupMapping[resourceGroup] = append(resourceGroupMapping[resourceGroup], fmt.Sprintf("%s:%s", zoneName, access))
		}
		for c.NextBlock() {
			switch c.Val() {
			case "subscription":
				if !c.NextArg() {
					return env, resourceGroupMapping, fall, c.ArgErr()
				}
				env.Values[auth.SubscriptionID] = c.Val()
			case "tenant":
				if !c.NextArg() {
					return env, resourceGroupMapping, fall, c.ArgErr()
				}
				env.Values[auth.TenantID] = c.Val()
			case "client":
				if !c.NextArg() {
					return env, resourceGroupMapping, fall, c.ArgErr()
				}
				env.Values[auth.ClientID] = c.Val()
			case "secret":
				if !c.NextArg() {
					return env, resourceGroupMapping, fall, c.ArgErr()
				}
				env.Values[auth.ClientSecret] = c.Val()
			case "environment":
				if !c.NextArg() {
					return env, resourceGroupMapping, fall, c.ArgErr()
				}
				env.Values[auth.ClientSecret] = c.Val()
				var err error
				if azureEnv, err = azurerest.EnvironmentFromName(c.Val()); err != nil {
					return env, resourceGroupMapping, fall, c.Errf("cannot set azure environment: %q", err.Error())
				}
			case "fallthrough":
				fall.SetZonesFromArgs(c.RemainingArgs())
			default:
				return env, resourceGroupMapping, fall, c.Errf("unknown property: %q", c.Val())
			}
		}
	}

	env.Values[auth.Resource] = azureEnv.ResourceManagerEndpoint
	env.Environment = azureEnv

	return env, resourceGroupMapping, fall, nil
}
