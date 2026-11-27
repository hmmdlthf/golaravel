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

// DeviceHeader carries the device id on pull and snapshot requests (push
// may send it in the body instead).
const DeviceHeader = "X-Sync-Device"

// Routes mounts the sync endpoints on r. Mount them behind your auth and
// tenant middleware and serve them over HTTPS only:
//
//	POST {r}/devices   register this device       -> Device
//	POST {r}/push      PushRequest                -> PushResponse
//	GET  {r}/pull      ?cursor=&limit=            -> PullResponse (410 = resync)
//	GET  {r}/snapshot  ?entity=&after=&limit=     -> SnapshotResponse
//
//	api := app.Group("/api/sync", auth.RequireJWT(jwt), privacy.TenantMiddleware(...))
//	eng.Routes(api)
func (e *Engine[T]) Routes(r router.RouteRegistrar) {
	h := e.Handlers()
	r.POST("/devices", h.RegisterDevice)
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
	var info DeviceInfo
	if err := h.decode(c, &info, 16<<10); err != nil {
		return h.fail(c, err)
	}
	d, err := h.e.RegisterDevice(c.Context(), p, info)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

// Push handles POST /push.
func (h Handlers[T]) Push(c *gcontext.Context) error {
	p, err := h.e.opts.Identify(c)
	if err != nil {
		return h.fail(c, err)
	}
	var req PushRequest
	if err := h.decode(c, &req, h.e.opts.MaxBody); err != nil {
		return h.fail(c, err)
	}
	if req.DeviceID == "" {
		req.DeviceID = c.Header(DeviceHeader)
	}
	resp, err := h.e.Push(c.Context(), p, &req)
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusOK, resp)
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
	resp, err := h.e.Pull(c.Context(), p, c.Header(DeviceHeader), cursor, int(limit))
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusOK, resp)
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
	resp, err := h.e.Snapshot(c.Context(), p, c.Header(DeviceHeader), c.Query("entity"), c.Query("after"), int(limit))
	if err != nil {
		return h.fail(c, err)
	}
	return c.JSON(http.StatusOK, resp)
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

func (h Handlers[T]) decode(c *gcontext.Context, dst any, limit int64) error {
	if c.Request.Body == nil {
		return badRequest{"empty body"}
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, limit+1))
	if err != nil {
		return badRequest{"unreadable body"}
	}
	if int64(len(body)) > limit {
		return ErrBatchTooLarge
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return badRequest{"invalid JSON"}
	}
	return nil
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
