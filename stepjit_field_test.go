package gozero

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestFieldChainNilPointer reads through a nil pointer step on both
// tiers: both must fail rather than fault, naming the nil type. The
// chain bottoms out at a frame slot, then derefs the pointer field
// fieldAddr loaded, so the nil check is the load's own.
func TestFieldChainNilPointer(t *testing.T) {
	rt := pairRuntime(t)
	const src = `
		var r http.Request;
		json.NewEncoder(dest).Encode(r.Response.StatusCode);
	`
	jit, slow := compilePair(t, rt, src)
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		_, err := fn(context.Background(), nil, &dest)
		if err == nil || !strings.Contains(err.Error(), "nil *http.Response") {
			t.Errorf("%s: err = %v, want a nil *http.Response read error", name, err)
		}
	}
}
