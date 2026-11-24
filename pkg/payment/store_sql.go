package payment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
)

// SQLStore keeps gateways, payments and their history in three tables
// (payment_gateways, payments, payment_logs by default). SQLite, MySQL and
// Postgres are supported; create the tables with Migrate.
type SQLStore struct {
	db       *sql.DB
	d        database.Dialect
	gateways string
	payments string
	logs     string
	methods  string
	subs     string
}

// NewSQLStore creates the store. prefix, when given, is put in front of
// the table names ("shop_" gives shop_payments...).
func NewSQLStore(db *sql.DB, dialect database.Dialect, prefix ...string) (*SQLStore, error) {
	p := ""
	if len(prefix) > 0 {
		p = prefix[0]
	}
	s := &SQLStore{db: db, d: dialect, gateways: p + "payment_gateways", payments: p + "payments", logs: p + "payment_logs",
		methods: p + "payment_methods", subs: p + "payment_subscriptions"}
	for _, t := range []string{s.gateways, s.payments, s.logs, s.methods, s.subs} {
		if !database.ValidIdentifier(t) {
			return nil, fmt.Errorf("payment: invalid table name %q", t)
		}
	}
	return s, nil
}

// Migrate creates the tables and indexes if they do not exist.
func (s *SQLStore) Migrate(ctx context.Context) error {
	k := s.d.KeyType()
	text := "TEXT"
	if s.d == database.MySQL {
		text = "LONGTEXT"
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, driver %s NOT NULL, name %s NOT NULL, enabled INTEGER NOT NULL DEFAULT 0,
			mode %s NOT NULL, auto_capture INTEGER NOT NULL DEFAULT 0, settings %s, sort INTEGER NOT NULL DEFAULT 0,
			created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`, s.gateways, k, k, k, k, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, reference %s NOT NULL, gateway_id %s NOT NULL, driver %s NOT NULL,
			status %s NOT NULL, amount BIGINT NOT NULL, currency %s NOT NULL, refunded BIGINT NOT NULL DEFAULT 0,
			gateway_ref %s, customer_name %s, customer_email %s, mode %s NOT NULL,
			version INTEGER NOT NULL DEFAULT 0, paid_at BIGINT, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL,
			extra %s, subscription_id %s)`, s.payments, k, k, k, k, k, k, k, k, k, k, text, k),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, gateway_id %s NOT NULL, status %s NOT NULL, customer_ref %s, customer_email %s,
			gateway_ref %s, version INTEGER NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, body %s NOT NULL)`,
			s.methods, k, k, k, k, k, k, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, gateway_id %s NOT NULL, status %s NOT NULL, billing %s NOT NULL, reference %s NOT NULL,
			customer_ref %s, customer_email %s, gateway_ref %s, next_billing_at BIGINT,
			version INTEGER NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, body %s NOT NULL)`,
			s.subs, k, k, k, k, k, k, k, k, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, payment_id %s NOT NULL, event %s NOT NULL, from_status %s, to_status %s,
			message %s, event_id %s, created_at BIGINT NOT NULL)`, s.logs, k, k, k, k, k, text, k),
	}
	idx := [][3]string{
		{s.payments, "reference", "reference"},
		{s.payments, "gref", "gateway_id, gateway_ref"},
		{s.payments, "created", "created_at"},
		{s.logs, "payment", "payment_id"},
		{s.payments, "sub", "subscription_id"},
		{s.methods, "customer", "customer_ref"},
		{s.subs, "gref", "gateway_id, gateway_ref"},
		{s.subs, "due", "billing, next_billing_at"},
	}
	for _, i := range idx {
		name := i[0] + "_" + i[1] + "_idx"
		if s.d == database.MySQL {
			// MySQL has no IF NOT EXISTS for indexes: ignore "duplicate key name"
			stmts = append(stmts, fmt.Sprintf("CREATE INDEX %s ON %s (%s)", name, i[0], i[2]))
		} else {
			stmts = append(stmts, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, i[0], i[2]))
		}
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			if s.d == database.MySQL && strings.Contains(err.Error(), "Duplicate key name") {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *SQLStore) q(query string) string { return s.d.Rebind(query) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *SQLStore) SaveGateway(ctx context.Context, g *GatewayConfig) error {
	settings, _ := json.Marshal(g.Settings)
	_, err := s.db.ExecContext(ctx, s.d.Upsert(s.gateways, "id", "driver", "name", "enabled", "mode",
		"auto_capture", "settings", "sort", "created_at", "updated_at"),
		g.ID, g.Driver, g.Name, b2i(g.Enabled), string(g.Mode), b2i(g.AutoCapture), string(settings), g.Sort,
		g.CreatedAt.UnixMilli(), g.UpdatedAt.UnixMilli())
	return err
}

func (s *SQLStore) DeleteGateway(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q("DELETE FROM "+s.gateways+" WHERE id = ?"), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const gatewayCols = "id, driver, name, enabled, mode, auto_capture, settings, sort, created_at, updated_at"

func scanGateway(row interface{ Scan(...any) error }) (*GatewayConfig, error) {
	var g GatewayConfig
	var enabled, auto int
	var mode string
	var settings sql.NullString
	var created, updated int64
	if err := row.Scan(&g.ID, &g.Driver, &g.Name, &enabled, &mode, &auto, &settings, &g.Sort, &created, &updated); err != nil {
		return nil, err
	}
	g.Enabled, g.AutoCapture, g.Mode = enabled == 1, auto == 1, Mode(mode)
	g.Settings = map[string]string{}
	if settings.Valid && settings.String != "" {
		_ = json.Unmarshal([]byte(settings.String), &g.Settings)
	}
	g.CreatedAt, g.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return &g, nil
}

func (s *SQLStore) Gateway(ctx context.Context, id string) (*GatewayConfig, error) {
	g, err := scanGateway(s.db.QueryRowContext(ctx, s.q("SELECT "+gatewayCols+" FROM "+s.gateways+" WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (s *SQLStore) Gateways(ctx context.Context) ([]*GatewayConfig, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+gatewayCols+" FROM "+s.gateways+" ORDER BY sort, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GatewayConfig
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// paymentExtra holds the payment fields that are never filtered on.
type paymentExtra struct {
	Description   string            `json:"description,omitempty"`
	CustomerPhone string            `json:"customer_phone,omitempty"`
	SuccessURL    string            `json:"success_url,omitempty"`
	CancelURL     string            `json:"cancel_url,omitempty"`
	Action        *Action           `json:"action,omitempty"`
	FailureReason string            `json:"failure_reason,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Data          map[string]string `json:"data,omitempty"`
	MethodID      string            `json:"payment_method_id,omitempty"`
}

const paymentCols = "id, reference, gateway_id, driver, status, amount, currency, refunded, gateway_ref, customer_name, customer_email, mode, version, paid_at, created_at, updated_at, extra, subscription_id"

func paymentArgs(p *Payment) []any {
	extra, _ := json.Marshal(paymentExtra{p.Description, p.CustomerPhone, p.SuccessURL, p.CancelURL,
		p.Action, p.FailureReason, p.Metadata, p.Data, p.PaymentMethodID})
	var paid any
	if p.PaidAt != nil {
		paid = p.PaidAt.UnixMilli()
	}
	return []any{p.ID, p.Reference, p.GatewayID, p.Driver, string(p.Status), p.Amount, p.Currency, p.Refunded,
		p.GatewayRef, p.CustomerName, p.CustomerEmail, string(p.Mode), p.Version, paid,
		p.CreatedAt.UnixMilli(), p.UpdatedAt.UnixMilli(), string(extra), p.SubscriptionID}
}

func scanPayment(row interface{ Scan(...any) error }) (*Payment, error) {
	var p Payment
	var status, mode string
	var gref, cname, cemail, extra, subID sql.NullString
	var paid sql.NullInt64
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Reference, &p.GatewayID, &p.Driver, &status, &p.Amount, &p.Currency, &p.Refunded,
		&gref, &cname, &cemail, &mode, &p.Version, &paid, &created, &updated, &extra, &subID); err != nil {
		return nil, err
	}
	p.Status, p.Mode = Status(status), Mode(mode)
	p.GatewayRef, p.CustomerName, p.CustomerEmail = gref.String, cname.String, cemail.String
	if paid.Valid {
		t := time.UnixMilli(paid.Int64)
		p.PaidAt = &t
	}
	p.CreatedAt, p.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	var x paymentExtra
	if extra.Valid {
		_ = json.Unmarshal([]byte(extra.String), &x)
	}
	p.Description, p.CustomerPhone, p.SuccessURL, p.CancelURL = x.Description, x.CustomerPhone, x.SuccessURL, x.CancelURL
	p.Action, p.FailureReason, p.Metadata, p.Data = x.Action, x.FailureReason, x.Metadata, x.Data
	p.PaymentMethodID, p.SubscriptionID = x.MethodID, subID.String
	return &p, nil
}

func (s *SQLStore) CreatePayment(ctx context.Context, p *Payment) error {
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(strings.Split(paymentCols, ", "))), ", ")
	_, err := s.db.ExecContext(ctx, s.q("INSERT INTO "+s.payments+" ("+paymentCols+") VALUES ("+ph+")"), paymentArgs(p)...)
	return err
}

func (s *SQLStore) UpdatePayment(ctx context.Context, p *Payment) error {
	args := paymentArgs(p)
	args[12] = p.Version + 1
	// SET every column but id, then WHERE id = ? AND version = ?
	set := []string{}
	for _, c := range strings.Split(paymentCols, ", ")[1:] {
		set = append(set, c+" = ?")
	}
	res, err := s.db.ExecContext(ctx, s.q("UPDATE "+s.payments+" SET "+strings.Join(set, ", ")+" WHERE id = ? AND version = ?"),
		append(args[1:], p.ID, p.Version)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Payment(ctx, p.ID); errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return ErrVersionConflict
	}
	p.Version++
	return nil
}

func (s *SQLStore) Payment(ctx context.Context, id string) (*Payment, error) {
	p, err := scanPayment(s.db.QueryRowContext(ctx, s.q("SELECT "+paymentCols+" FROM "+s.payments+" WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *SQLStore) PaymentByGatewayRef(ctx context.Context, gatewayID, ref string) (*Payment, error) {
	if ref == "" {
		return nil, ErrNotFound
	}
	p, err := scanPayment(s.db.QueryRowContext(ctx, s.q("SELECT "+paymentCols+" FROM "+s.payments+
		" WHERE gateway_id = ? AND gateway_ref = ? ORDER BY created_at DESC LIMIT 1"), gatewayID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *SQLStore) Payments(ctx context.Context, q PaymentQuery) ([]*Payment, int, error) {
	var where []string
	var args []any
	add := func(cond string, v any) { where = append(where, cond); args = append(args, v) }
	if q.Status != "" {
		add("status = ?", string(q.Status))
	}
	if q.GatewayID != "" {
		add("gateway_id = ?", q.GatewayID)
	}
	if q.Reference != "" {
		add("reference = ?", q.Reference)
	}
	if q.Mode != "" {
		add("mode = ?", string(q.Mode))
	}
	if q.SubscriptionID != "" {
		add("subscription_id = ?", q.SubscriptionID)
	}
	if s := strings.TrimSpace(q.Search); s != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(s)) + "%"
		where = append(where, `(LOWER(id) LIKE ? ESCAPE '\' OR LOWER(reference) LIKE ? ESCAPE '\' OR LOWER(gateway_ref) LIKE ? ESCAPE '\' OR LOWER(customer_email) LIKE ? ESCAPE '\' OR LOWER(customer_name) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like, like)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM "+s.payments+w), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "created_at"
	switch q.Sort {
	case "amount", "status":
		order = q.Sort
	}
	dir := " ASC"
	if q.Desc || q.Sort == "" {
		dir = " DESC"
	}
	query := "SELECT " + paymentCols + " FROM " + s.payments + w + " ORDER BY " + order + dir + ", id" + dir
	if q.PerPage > 0 {
		page := q.Page
		if page < 1 {
			page = 1
		}
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", q.PerPage, (page-1)*q.PerPage)
	}
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func (s *SQLStore) AddLog(ctx context.Context, l *Log) error {
	_, err := s.db.ExecContext(ctx, s.q("INSERT INTO "+s.logs+
		" (id, payment_id, event, from_status, to_status, message, event_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"),
		l.ID, l.PaymentID, l.Event, string(l.From), string(l.To), l.Message, l.EventID, l.CreatedAt.UnixMilli())
	return err
}

func (s *SQLStore) Logs(ctx context.Context, paymentID string) ([]*Log, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT id, payment_id, event, from_status, to_status, message, event_id, created_at FROM "+
		s.logs+" WHERE payment_id = ? ORDER BY created_at, id"), paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Log
	for rows.Next() {
		var l Log
		var from, to, msg, eid sql.NullString
		var at int64
		if err := rows.Scan(&l.ID, &l.PaymentID, &l.Event, &from, &to, &msg, &eid, &at); err != nil {
			return nil, err
		}
		l.From, l.To, l.Message, l.EventID = Status(from.String), Status(to.String), msg.String, eid.String
		l.CreatedAt = time.UnixMilli(at)
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (s *SQLStore) SeenEvent(ctx context.Context, paymentID, eventID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM "+s.logs+" WHERE payment_id = ? AND event_id = ?"),
		paymentID, eventID).Scan(&n)
	return n > 0, err
}

// ---- saved methods and subscriptions ---------------------------------------
// Both tables keep the full record as JSON in body, with copies of the
// columns they are filtered on.

func ms(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func (s *SQLStore) CreateMethod(ctx context.Context, m *PaymentMethod) error {
	b, _ := json.Marshal(m)
	_, err := s.db.ExecContext(ctx, s.q("INSERT INTO "+s.methods+
		" (id, gateway_id, status, customer_ref, customer_email, gateway_ref, version, created_at, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		m.ID, m.GatewayID, string(m.Status), m.CustomerRef, m.CustomerEmail, m.GatewayRef, m.Version, m.CreatedAt.UnixMilli(), string(b))
	return err
}

func (s *SQLStore) UpdateMethod(ctx context.Context, m *PaymentMethod) error {
	m.Version++
	b, _ := json.Marshal(m)
	res, err := s.db.ExecContext(ctx, s.q("UPDATE "+s.methods+
		" SET status = ?, customer_ref = ?, customer_email = ?, gateway_ref = ?, version = ?, body = ? WHERE id = ? AND version = ?"),
		string(m.Status), m.CustomerRef, m.CustomerEmail, m.GatewayRef, m.Version, string(b), m.ID, m.Version-1)
	if err != nil {
		m.Version--
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		m.Version--
		if _, err := s.Method(ctx, m.ID); errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return ErrVersionConflict
	}
	return nil
}

func scanBody[T any](row interface{ Scan(...any) error }) (*T, error) {
	var body string
	if err := row.Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *SQLStore) Method(ctx context.Context, id string) (*PaymentMethod, error) {
	return scanBody[PaymentMethod](s.db.QueryRowContext(ctx, s.q("SELECT body FROM "+s.methods+" WHERE id = ?"), id))
}

type where struct {
	conds []string
	args  []any
}

func (w *where) eq(col, v string) {
	if v != "" {
		w.conds = append(w.conds, col+" = ?")
		w.args = append(w.args, v)
	}
}

func (w *where) search(q string, cols ...string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(q)) + "%"
	var ors []string
	for _, c := range cols {
		ors = append(ors, "LOWER("+c+`) LIKE ? ESCAPE '\'`)
		w.args = append(w.args, like)
	}
	w.conds = append(w.conds, "("+strings.Join(ors, " OR ")+")")
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

func limitOffset(page, perPage int) string {
	if perPage <= 0 {
		return ""
	}
	if page < 1 {
		page = 1
	}
	return fmt.Sprintf(" LIMIT %d OFFSET %d", perPage, (page-1)*perPage)
}

func listBodies[T any](ctx context.Context, s *SQLStore, table string, w *where, order string, page, perPage int) ([]*T, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM "+table+w.sql()), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, s.q("SELECT body FROM "+table+w.sql()+" ORDER BY "+order+limitOffset(page, perPage)), w.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*T
	for rows.Next() {
		v, err := scanBody[T](rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (s *SQLStore) Methods(ctx context.Context, q MethodQuery) ([]*PaymentMethod, int, error) {
	w := &where{}
	w.eq("status", string(q.Status))
	w.eq("gateway_id", q.GatewayID)
	w.eq("customer_ref", q.CustomerRef)
	w.search(q.Search, "id", "customer_ref", "customer_email", "gateway_ref")
	return listBodies[PaymentMethod](ctx, s, s.methods, w, "created_at DESC, id DESC", q.Page, q.PerPage)
}

func (s *SQLStore) CreateSubscription(ctx context.Context, sub *Subscription) error {
	b, _ := json.Marshal(sub)
	_, err := s.db.ExecContext(ctx, s.q("INSERT INTO "+s.subs+
		" (id, gateway_id, status, billing, reference, customer_ref, customer_email, gateway_ref, next_billing_at, version, created_at, body)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		sub.ID, sub.GatewayID, string(sub.Status), string(sub.Billing), sub.Reference, sub.CustomerRef, sub.CustomerEmail,
		sub.GatewayRef, ms(sub.NextBillingAt), sub.Version, sub.CreatedAt.UnixMilli(), string(b))
	return err
}

func (s *SQLStore) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	sub.Version++
	b, _ := json.Marshal(sub)
	res, err := s.db.ExecContext(ctx, s.q("UPDATE "+s.subs+
		" SET status = ?, billing = ?, reference = ?, customer_ref = ?, customer_email = ?, gateway_ref = ?, next_billing_at = ?, version = ?, body = ?"+
		" WHERE id = ? AND version = ?"),
		string(sub.Status), string(sub.Billing), sub.Reference, sub.CustomerRef, sub.CustomerEmail, sub.GatewayRef,
		ms(sub.NextBillingAt), sub.Version, string(b), sub.ID, sub.Version-1)
	if err != nil {
		sub.Version--
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		sub.Version--
		if _, err := s.Subscription(ctx, sub.ID); errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return ErrVersionConflict
	}
	return nil
}

func (s *SQLStore) Subscription(ctx context.Context, id string) (*Subscription, error) {
	return scanBody[Subscription](s.db.QueryRowContext(ctx, s.q("SELECT body FROM "+s.subs+" WHERE id = ?"), id))
}

func (s *SQLStore) SubscriptionByGatewayRef(ctx context.Context, gatewayID, ref string) (*Subscription, error) {
	if ref == "" {
		return nil, ErrNotFound
	}
	return scanBody[Subscription](s.db.QueryRowContext(ctx, s.q("SELECT body FROM "+s.subs+
		" WHERE gateway_id = ? AND gateway_ref = ? ORDER BY created_at DESC LIMIT 1"), gatewayID, ref))
}

func (s *SQLStore) Subscriptions(ctx context.Context, q SubscriptionQuery) ([]*Subscription, int, error) {
	w := &where{}
	w.eq("status", string(q.Status))
	w.eq("gateway_id", q.GatewayID)
	w.eq("reference", q.Reference)
	w.eq("customer_ref", q.CustomerRef)
	w.eq("billing", string(q.Billing))
	w.search(q.Search, "id", "reference", "gateway_ref", "customer_email", "customer_ref")
	return listBodies[Subscription](ctx, s, s.subs, w, "created_at DESC, id DESC", q.Page, q.PerPage)
}

func (s *SQLStore) DueSubscriptions(ctx context.Context, now time.Time, limit int) ([]*Subscription, error) {
	w := &where{conds: []string{"billing = ?", "status IN (?, ?)", "next_billing_at IS NOT NULL", "next_billing_at <= ?"},
		args: []any{string(BillingManager), string(SubActive), string(SubPastDue), now.UnixMilli()}}
	out, _, err := listBodies[Subscription](ctx, s, s.subs, w, "next_billing_at, id", 1, limit)
	return out, err
}
