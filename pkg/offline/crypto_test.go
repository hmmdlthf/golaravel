package offline_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func TestKeyDerivationAndEnvelopes(t *testing.T) {
	sk, err := offline.GenerateServerKey("k1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := offline.ParseServerKey("k1", sk.Encode())
	if err != nil || again.PublicKey() != sk.PublicKey() {
		t.Fatalf("server key round trip: %v", err)
	}
	dev, _ := ecdh.P256().GenerateKey(rand.Reader)
	k1, _ := offline.DeriveKey(sk.Private, dev.PublicKey(), "dev-1")
	k2, _ := offline.DeriveKey(dev, sk.Private.PublicKey(), "dev-1")
	k3, _ := offline.DeriveKey(dev, sk.Private.PublicKey(), "dev-2")
	if !bytes.Equal(k1, k2) || len(k1) != 32 || bytes.Equal(k1, k3) {
		t.Fatal("both sides must derive the same 32-byte key, bound to the device id")
	}

	aad := offline.AAD("req", "push", "acme", "dev-1")
	env, err := offline.Seal(k1, "k1", "dev-1", aad, []byte(`{"secret":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := offline.Open(k2, env, aad); err != nil || string(pt) != `{"secret":1}` {
		t.Fatalf("open: %q %v", pt, err)
	}
	for name, other := range map[string][]byte{
		"other endpoint":  offline.AAD("req", "pull", "acme", "dev-1"),
		"other tenant":    offline.AAD("req", "push", "globex", "dev-1"),
		"other direction": offline.AAD("res", "push", "acme", "dev-1"),
	} {
		if _, err := offline.Open(k1, env, other); !errors.Is(err, offline.ErrBadEnvelope) {
			t.Errorf("%s: envelope must not open (%v)", name, err)
		}
	}
	tampered := *env
	ct, _ := base64.RawURLEncoding.DecodeString(env.CT)
	ct[0] ^= 1
	tampered.CT = base64.RawURLEncoding.EncodeToString(ct)
	if _, err := offline.Open(k1, &tampered, aad); !errors.Is(err, offline.ErrBadEnvelope) {
		t.Fatal("tampered ciphertext opened")
	}
}

func TestDeviceKeyRegistration(t *testing.T) {
	ctx := context.Background()
	p := offline.Principal{TenantID: "acme", UserID: "u"}
	devKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	pub := base64.RawURLEncoding.EncodeToString(devKey.PublicKey().Bytes())

	plain := newEnv(t)
	if _, err := plain.eng.RegisterDevice(ctx, p, offline.DeviceInfo{PublicKey: pub}); !errors.Is(err, offline.ErrEncryptionUnavailable) {
		t.Fatalf("key without server keys: %v", err)
	}

	sk, _ := offline.GenerateServerKey("k1")
	e := newEnv(t, func(o *offline.Options[*testent.Tx]) { o.Keys = []offline.ServerKey{sk} })
	d, err := e.eng.RegisterDevice(ctx, p, offline.DeviceInfo{PublicKey: pub})
	if err != nil || !d.Encrypted || d.ServerKey == nil || d.ServerKey.PublicKey != sk.PublicKey() || d.ServerKey.KeyID != "k1" {
		t.Fatalf("register: %+v %v", d, err)
	}
	if _, err := e.eng.RegisterDevice(ctx, p, offline.DeviceInfo{PublicKey: "not-a-key"}); !errors.Is(err, offline.ErrBadPublicKey) {
		t.Fatalf("bad key: %v", err)
	}
	stored, _ := e.eng.Device(ctx, "acme", d.ID)
	if !stored.Encrypted || stored.ServerKey != nil {
		t.Fatalf("stored device: %+v", stored)
	}

	if _, err := offline.New(offline.Options[*testent.Tx]{Begin: func(context.Context) (*testent.Tx, error) { return nil, nil },
		RequireEncryption: true}); err == nil {
		t.Fatal("RequireEncryption without keys accepted")
	}
	strict := newEnv(t, func(o *offline.Options[*testent.Tx]) { o.Keys = []offline.ServerKey{sk}; o.RequireEncryption = true })
	if _, err := strict.eng.RegisterDevice(ctx, p, offline.DeviceInfo{}); !errors.Is(err, offline.ErrEncryptionRequired) {
		t.Fatalf("keyless device under RequireEncryption: %v", err)
	}
}

// encClient is a test device that encrypts like the TypeScript client.
type encClient struct {
	t      *testing.T
	srv    *httptest.Server
	priv   *ecdh.PrivateKey
	dev    offline.Device
	key    []byte
	tenant string
}

func newSyncServer(t *testing.T, e *env) *httptest.Server {
	app := golaravel.New()
	signIn := func(c *gcontext.Context) error {
		c.Set(auth.ContextUserKey, &auth.SimpleUser{ID: "cashier"})
		c.Request = c.Request.WithContext(privacy.WithTenantID(c.Context(), c.Header("X-Test-Tenant")))
		return c.Next()
	}
	e.eng.Routes(app.Router.Group("/sync", signIn))
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func (c *encClient) do(method, path string, body []byte) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.srv.URL+"/sync"+path, rd)
	req.Header.Set("X-Test-Tenant", c.tenant)
	if c.dev.ID != "" {
		req.Header.Set(offline.DeviceHeader, c.dev.ID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c *encClient) register() {
	c.t.Helper()
	c.priv, _ = ecdh.P256().GenerateKey(rand.Reader)
	body, _ := json.Marshal(offline.DeviceInfo{Name: "till", PublicKey: base64.RawURLEncoding.EncodeToString(c.priv.PublicKey().Bytes())})
	code, b := c.do("POST", "/devices", body)
	if code != 201 {
		c.t.Fatalf("register: %d %s", code, b)
	}
	_ = json.Unmarshal(b, &c.dev)
	raw, _ := base64.RawURLEncoding.DecodeString(c.dev.ServerKey.PublicKey)
	spub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		c.t.Fatal(err)
	}
	c.key, _ = offline.DeriveKey(c.priv, spub, c.dev.ID)
}

func (c *encClient) seal(endpoint string, v any) []byte {
	b, _ := json.Marshal(v)
	env, _ := offline.Seal(c.key, c.dev.ServerKey.KeyID, c.dev.ID, offline.AAD("req", endpoint, c.tenant, c.dev.ID), b)
	out, _ := json.Marshal(env)
	return out
}

func (c *encClient) open(endpoint string, body []byte, v any) {
	c.t.Helper()
	var env offline.Envelope
	if err := json.Unmarshal(body, &env); err != nil || env.CT == "" {
		c.t.Fatalf("response is not an envelope: %s", body)
	}
	pt, err := offline.Open(c.key, &env, offline.AAD("res", endpoint, c.tenant, c.dev.ID))
	if err != nil {
		c.t.Fatalf("open %s response: %v", endpoint, err)
	}
	if err := json.Unmarshal(pt, v); err != nil {
		c.t.Fatal(err)
	}
}

func errCode(b []byte) string {
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m["error"]
}

func TestEncryptedSyncOverHTTP(t *testing.T) {
	sk, _ := offline.GenerateServerKey("k1")
	e := newEnv(t, func(o *offline.Options[*testent.Tx]) { o.Keys = []offline.ServerKey{sk} })
	c := &encClient{t: t, srv: newSyncServer(t, e), tenant: "acme"}
	c.register()
	clk := newClock(c.dev.ID, e.now)

	req := offline.PushRequest{Mutations: []offline.Mutation{create(clk, "p1", set("name", "Secret tea", "stock", 4))}}
	code, b := c.do("POST", "/push", c.seal("push", req))
	if code != 200 {
		t.Fatalf("encrypted push: %d %s", code, b)
	}
	if bytes.Contains(b, []byte(`"p1"`)) { // quotes never occur in base64
		t.Fatal("push response leaked plaintext")
	}
	var pushed offline.PushResponse
	c.open("push", b, &pushed)
	if pushed.Results[0].Status != offline.StatusApplied {
		t.Fatalf("push result: %+v", pushed.Results[0])
	}

	code, b = c.do("GET", "/pull?cursor=0", nil)
	if code != 200 || bytes.Contains(b, []byte("Secret tea")) {
		t.Fatalf("pull: %d %s", code, b)
	}
	var pulled offline.PullResponse
	c.open("pull", b, &pulled)
	if len(pulled.Changes) != 1 || pulled.Changes[0].EntityID != "p1" {
		t.Fatalf("pulled %+v", pulled)
	}
	code, b = c.do("GET", "/snapshot?entity=product", nil)
	var snap offline.SnapshotResponse
	if code != 200 {
		t.Fatalf("snapshot: %d", code)
	}
	c.open("snapshot", b, &snap)

	// Plaintext from an encrypting device is refused: knowing the device
	// id is not enough to push.
	plain, _ := json.Marshal(req)
	if code, b = c.do("POST", "/push", plain); code != 403 || errCode(b) != "encryption_required" {
		t.Fatalf("plaintext push: %d %s", code, b)
	}
	// An envelope made for another endpoint does not open here.
	if code, b = c.do("POST", "/push", c.seal("pull", req)); code != 400 || errCode(b) != "bad_envelope" {
		t.Fatalf("misdirected envelope: %d %s", code, b)
	}
	// Nor one sealed with someone else's key.
	thief := *c
	thief.key = make([]byte, 32)
	if code, b = c.do("POST", "/push", thief.seal("push", req)); code != 400 {
		t.Fatalf("wrong key: %d %s", code, b)
	}

	// Signing the device out revokes it and drops its key.
	if code, _ = c.do("DELETE", "/devices/current", nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, b = c.do("GET", "/pull?cursor=0", nil); code != 403 || errCode(b) != "device_revoked" {
		t.Fatalf("pull after revoke: %d %s", code, b)
	}
	if code, b = c.do("POST", "/push", c.seal("push", req)); code != 403 || errCode(b) != "device_revoked" {
		t.Fatalf("push after revoke: %d %s", code, b)
	}
}

func TestServerKeyRotation(t *testing.T) {
	old, _ := offline.GenerateServerKey("k1")
	cur, _ := offline.GenerateServerKey("k2")
	e := newEnv(t, func(o *offline.Options[*testent.Tx]) { o.Keys = []offline.ServerKey{old} })
	srv := newSyncServer(t, e)
	c := &encClient{t: t, srv: srv, tenant: "acme"}
	c.register()

	// Rotate: k2 is current, k1 stays listed. The old device still works.
	rotated, err := offline.New(offline.Options[*testent.Tx]{
		Begin: func(ctx context.Context) (*testent.Tx, error) { return e.client.Tx(ctx) },
		Keys:  []offline.ServerKey{cur, old},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = rotated.Register(e.productResource())
	e.eng = rotated
	c.srv = newSyncServer(t, e)
	if code, b := c.do("GET", "/pull", nil); code != 200 {
		t.Fatalf("old device after rotation: %d %s", code, b)
	}
	fresh := &encClient{t: t, srv: c.srv, tenant: "acme"}
	fresh.register()
	if fresh.dev.ServerKey.KeyID != "k2" {
		t.Fatalf("new devices should use the current key: %s", fresh.dev.ServerKey.KeyID)
	}

	// Retire k1: its devices are told to register again.
	retired, _ := offline.New(offline.Options[*testent.Tx]{
		Begin: func(ctx context.Context) (*testent.Tx, error) { return e.client.Tx(ctx) },
		Keys:  []offline.ServerKey{cur},
	})
	_ = retired.Register(e.productResource())
	e.eng = retired
	c.srv = newSyncServer(t, e)
	if code, b := c.do("GET", "/pull", nil); code != 403 || errCode(b) != "reregister" {
		t.Fatalf("retired key: %d %s", code, b)
	}
}
