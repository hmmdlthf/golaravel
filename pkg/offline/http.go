package offline

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/privacy"
	"github.com/golaravel/golaravel/pkg/router"
)

// DeviceHeader carries the device id on every request after registration
// (push may send it in the body or envelope instead).
const DeviceHeader = "X-Sync-Device"

// Routes mounts the sync endpoints on r. Mount them behind your auth and
// tenant middleware and serve them over HTTPS only:
//
//	POST   {r}/devices          register this device      -> Device (+ server_key)
//	GET    {r}/devices/current  this device               -> Device
//	DELETE {r}/devices/current  sign this device out (revoke)
//	POST   {r}/push             PushRequest               -> PushResponse
//	GET    {r}/pull             ?cursor=&limit=           -> PullResponse (410 = resync)
//	GET    {r}/snapshot         ?entity=&after=&limit=    -> SnapshotResponse
//
// For a device registered with a public key, push bodies must be
// Envelopes and push, pull and snapshot responses are Envelopes.
//
//	api := app.Group("/api/sync", auth.RequireJWT(jwt), privacy.TenantMiddleware(...))
//	eng.Routes(api)
func (e *Engine[T]) Routes(r router.RouteRegistrar) {
	h := e.Handlers()
	r.POST("/devices", h.RegisterDevice)
	r.GET("/devices/current", h.CurrentDevice)
	r.DELETE("/devices/current", h.RevokeCurrentDevice)
	r.POST("/push", h.Push)
	r.GET("/pull", h.Pull)
	r.GET("/snapshot", h.Snapshot)
}

// Handlers are the sync endpoints, for mounting them yourself.
type Handlers[T Tx] struct{ e *Engine[T] }

// Handlers returns the sync endpoints.
func (e *Engine[T]) Handlers() Handlers[T] { return Handlers[T]{e} }

// DefaultIdentify takes the tenant set by privacy.TenantMiddleware and the
// user set by the auth guards.
func DefaultIdentify(c *gcontext.Context) (Principal, error) {
	tenant, _ := privacy.TenantFromContext(c.Context())
	if tenant == "" {
		return Principal{}, ErrNoTenant
	}
	var user string
	if v, ok := c.Get(auth.ContextUserKey); ok {
		if a, ok := v.(auth.Authenticatable); ok {
			user = a.GetAuthIdentifier()
		}
	}
	if user == "" {
		return Principal{}, ErrUnauthorized
	}
	return Principal{TenantID: tenant, UserID: user}, nil
}

// RegisterDevice handles POST /devices.
func (h Handlers[T]) RegisterDevice(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	body, err := readBody(c, 16<<10)
	if err != nil {
		return h.fail(c, err)
	}
	var info DeviceInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return h.fail(c, badRequest{"invalid JSON"})
	}
	d, err := h.e.RegisterDevice(c.Context(), p, info)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

// CurrentDevice handles GET /devices/current.
func (h Handlers[T]) CurrentDevice(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	d, err := h.e.Device(c.Context(), p.TenantID, c.Header(DeviceHeader))
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// RevokeCurrentDevice handles DELETE /devices/current.
func (h Handlers[T]) RevokeCurrentDevice(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	if err := h.e.RevokeDevice(c.Context(), p.TenantID, c.Header(DeviceHeader)); err != nil {
		return h.fail(c, err)
	}
	c.Response.WriteHeader(http.StatusNoContent)
	return nil
}

// Push handles POST /push.
func (h Handlers[T]) Push(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	body, err := readBody(c, h.e.opts.MaxBody)
	if err != nil {
		return h.fail(c, err)
	}
	var probe struct {
		CT       string `json:"ct"`
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return h.fail(c, badRequest{"invalid JSON"})
	}
	deviceID := probe.DeviceID
	if deviceID == "" {
		deviceID = c.Header(DeviceHeader)
	}
	ch, err := h.channel(c, p, deviceID)
	if err != nil {
		return h.fail(c, err)
	}
	if body, err = ch.open(probe.CT != "", body, "push"); err != nil {
		return h.fail(c, err)
	}
	var req PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return h.fail(c, badRequest{"invalid JSON"})
	}
	if req.DeviceID != "" && req.DeviceID != ch.dev.ID {
		return h.fail(c, badRequest{"device_id does not match"})
	}
	req.DeviceID = ch.dev.ID
	resp, err := h.e.Push(c.Context(), p, &req)
	if err != nil {
		return h.fail(c, err)
	}
	return h.reply(c, ch, "push", resp)
}

// Pull handles GET /pull?cursor=&limit=.
func (h Handlers[T]) Pull(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	cursor, err := intQuery(c, "cursor")
	if err != nil {
		return h.fail(c, err)
	}
	limit, err := intQuery(c, "limit")
	if err != nil {
		return h.fail(c, err)
	}
	ch, err := h.channel(c, p, c.Header(DeviceHeader))
	if err != nil {
		return h.fail(c, err)
	}
	resp, err := h.e.Pull(c.Context(), p, ch.dev.ID, cursor, int(limit))
	if err != nil {
		return h.fail(c, err)
	}
	return h.reply(c, ch, "pull", resp)
}

// Snapshot handles GET /snapshot?entity=&after=&limit=.
func (h Handlers[T]) Snapshot(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	limit, err := intQuery(c, "limit")
	if err != nil {
		return h.fail(c, err)
	}
	ch, err := h.channel(c, p, c.Header(DeviceHeader))
	if err != nil {
		return h.fail(c, err)
	}
	resp, err := h.e.Snapshot(c.Context(), p, ch.dev.ID, c.Query("entity"), c.Query("after"), int(limit))
	if err != nil {
		return h.fail(c, err)
	}
	return h.reply(c, ch, "snapshot", resp)
}

// channel is a request's device and, for encrypting devices, its key.
type channel struct {
	tenant string
	dev    *Device
	key    []byte
	kid    string
}

func (h Handlers[T]) channel(c *gcontext.Context, p Principal, deviceID string) (*channel, error) {
	dev, err := h.e.Device(c.Context(), p.TenantID, deviceID)
	if err != nil {
		return nil, err
	}
	if dev.RevokedAt != 0 {
		return nil, ErrDeviceRevoked
	}
	ch := &channel{tenant: p.TenantID, dev: dev}
	if dev.Encrypted {
		if ch.key, ch.kid, err = h.e.deviceSecret(dev); err != nil {
			return nil, err
		}
	}
	return ch, nil
}

// open returns the plaintext body: the decrypted envelope for encrypting
// devices (which may not send plaintext), the body as is for the rest.
func (ch *channel) open(enveloped bool, body []byte, endpoint string) ([]byte, error) {
	if ch.key == nil {
		if enveloped {
			return nil, ErrBadEnvelope
		}
		return body, nil
	}
	if !enveloped {
		return nil, ErrEncryptionRequired
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil || env.DeviceID != ch.dev.ID || env.KeyID != ch.kid {
		return nil, ErrBadEnvelope
	}
	return Open(ch.key, &env, AAD("req", endpoint, ch.tenant, ch.dev.ID))
}

func (h Handlers[T]) reply(c *gcontext.Context, ch *channel, endpoint string, v any) error {
	if ch.key == nil {
		return c.JSON(http.StatusOK, v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return h.fail(c, err)
	}
	env, err := Seal(ch.key, ch.kid, ch.dev.ID, AAD("res", endpoint, ch.tenant, ch.dev.ID), b)
	if err != nil {
		return h.fail(c, err)
	}
	c.SetHeader("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, env)
}

type badRequest struct{ msg string }

func (b badRequest) Error() string { return b.msg }

func intQuery(c *gcontext.Context, name string) (int64, error) {
	s := c.Query(name)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, badRequest{name + " must be a non-negative integer"}
	}
	return n, nil
}

func readBody(c *gcontext.Context, limit int64) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, badRequest{"empty body"}
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, limit+1))
	if err != nil {
		return nil, badRequest{"unreadable body"}
	}
	if int64(len(body)) > limit {
		return nil, ErrBatchTooLarge
	}
	return body, nil
}

func (h Handlers[T]) fail(c *gcontext.Context, err error) error {
	status, code := http.StatusInternalServerError, "server_error"
	msg := "sync failed"
	var br badRequest
	switch {
	case errors.As(err, &br):
		status, code, msg = http.StatusBadRequest, "bad_request", br.msg
	case errors.Is(err, ErrUnauthorized):
		status, code, msg = http.StatusUnauthorized, "unauthenticated", err.Error()
	case errors.Is(err, ErrNoTenant):
		status, code, msg = http.StatusForbidden, "no_tenant", err.Error()
	case errors.Is(err, ErrUnknownDevice):
		status, code, msg = http.StatusForbidden, "unknown_device", err.Error()
	case errors.Is(err, ErrDeviceRevoked):
		status, code, msg = http.StatusForbidden, "device_revoked", err.Error()
	case errors.Is(err, ErrEncryptionRequired):
		status, code, msg = http.StatusForbidden, "encryption_required", err.Error()
	case errors.Is(err, ErrUnknownServerKey):
		status, code, msg = http.StatusForbidden, "reregister", ErrUnknownServerKey.Error()
	case errors.Is(err, ErrBadEnvelope):
		status, code, msg = http.StatusBadRequest, "bad_envelope", err.Error()
	case errors.Is(err, ErrBadPublicKey), errors.Is(err, ErrEncryptionUnavailable):
		status, code, msg = http.StatusBadRequest, "bad_public_key", err.Error()
	case errors.Is(err, ErrResyncRequired):
		status, code, msg = http.StatusGone, "resync_required", err.Error()
	case errors.Is(err, ErrUnknownEntity):
		status, code, msg = http.StatusNotFound, "unknown_entity", err.Error()
	case errors.Is(err, ErrBatchTooLarge):
		status, code, msg = http.StatusRequestEntityTooLarge, "batch_too_large", err.Error()
	default:
		if h.e.opts.Classify(err) == Transient {
			status, code, msg = http.StatusServiceUnavailable, "unavailable", "try again later"
			c.SetHeader("Retry-After", "5")
		}
		h.e.log.Error("offline: request failed", "path", c.Request.URL.Path, "error", err)
	}
	return c.JSON(status, map[string]string{"error": code, "message": msg})
}
