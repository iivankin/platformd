package daemon

import (
	"context"
	"net/netip"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/internaldns"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/state"
)

type objectStoreDetailsStub struct {
	remaining state.ObjectStore
}

func (stub objectStoreDetailsStub) Stores(context.Context, string) ([]state.ObjectStore, error) {
	return []state.ObjectStore{stub.remaining}, nil
}

func (stub objectStoreDetailsStub) Details(context.Context, string, string) (objectstore.StoreDetails, error) {
	return objectstore.StoreDetails{
		Store: stub.remaining, Credential: state.S3Credential{Permission: "read_write"},
		AccessKey: "remaining-access", Secret: "remaining-secret",
	}, nil
}

type objectStoreDataPlaneStub struct {
	stores []objectstore.DataPlaneStore
}

func (*objectStoreDataPlaneStub) ReconcileBuckets(context.Context, []string) error { return nil }
func (stub *objectStoreDataPlaneStub) ConfigureDataPlaneProject(
	_ context.Context,
	_, _ string,
	stores []objectstore.DataPlaneStore,
) error {
	stub.stores = stores
	return nil
}
func (*objectStoreDataPlaneStub) RemoveDataPlaneProject(context.Context, string) error { return nil }
func (*objectStoreDataPlaneStub) BeginDataPlaneQuiesce(context.Context) error          { return nil }
func (*objectStoreDataPlaneStub) EndDataPlaneQuiesce(context.Context) error            { return nil }

func TestDisableObjectStoreRefreshesDataPlaneAndWithdrawsDNS(t *testing.T) {
	t.Parallel()
	deleted := state.ObjectStore{
		ID: "deleted", ProjectID: "project", ProjectName: "shop", Name: "old",
	}
	remaining := state.ObjectStore{
		ID: "remaining", ProjectID: "project", ProjectName: "shop", Name: "assets",
		BucketName: "assets-bucket",
	}
	zone, err := internaldns.NewZone(map[string]netip.Addr{
		"old.shop.internal": netip.MustParseAddr("10.90.0.1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dataPlane := &objectStoreDataPlaneStub{}
	runtime := &runtimeStack{
		firewall:             firewall.New(),
		firewallProjects:     map[string]firewall.Project{"project": {ID: "project", ObjectStoreEnabled: true}},
		dnsZones:             map[string]*internaldns.Zone{"project": zone},
		projectNetworks:      map[string]containerengine.Network{"project": {Gateway: "10.90.0.1"}},
		objectStoreDetails:   objectStoreDetailsStub{remaining: remaining},
		objectStoreDataPlane: dataPlane,
		objectStoreProjects:  map[string]bool{"project": true},
		objectStoreFailures:  make(map[string]error),
	}

	if err := runtime.DisableObjectStore(context.Background(), deleted); err != nil {
		t.Fatal(err)
	}
	if len(dataPlane.stores) != 1 || dataPlane.stores[0].StoreID != remaining.ID ||
		dataPlane.stores[0].AccessKey != "remaining-access" {
		t.Fatalf("configured stores = %+v", dataPlane.stores)
	}
	if _, exists := zone.Lookup("old.shop.internal"); exists {
		t.Fatal("deleted object store DNS record is still published")
	}
}
