package hostagent

import (
	"context"
	"testing"
)

func TestAssetCommandChecks(t *testing.T) {
	for _, c := range []AssetCmd{{Set: "skins", File: "../x.webp"}, {Set: "other", File: "de_dust2.webp"}, {Set: "skins", File: "weapon_ak47"}} {
		if err := validateCommand(TypeAsset, &c); err == nil {
			t.Errorf("%+v passed", c)
		}
	}
	if err := validateCommand(TypeAsset, &AssetCmd{Set: "maps-480", File: "de_dust2_1.webp"}); err != nil {
		t.Error(err)
	}
	a := New(Options{})
	if r := a.handleAsset(context.Background(), NewULID(), &AssetCmd{Set: "skins", File: "x.webp"}); r.Error == nil || r.Error.Code != CodeUnsupported {
		t.Errorf("no install: %+v", r)
	}
}
