package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/queue"
)

// Manager coordinates multi-channel notification dispatching.
type Manager struct {
	mu            sync.RWMutex
	channels      map[string]Channel
	databaseStore DatabaseStore
	queuePool     *queue.Queue
}

// NewManager creates a notification manager populated with default channels.
func NewManager() *Manager {
	dbStore := NewMemoryDatabaseStore()
	m := &Manager{
		channels:      make(map[string]Channel),
		databaseStore: dbStore,
		queuePool:     queue.DefaultQueue(),
	}

	// Register standard built-in channels
	m.RegisterChannel("mail", NewMailChannel(nil))
	m.RegisterChannel("database", NewDatabaseChannel(dbStore))
	m.RegisterChannel("broadcast", NewBroadcastChannel(broadcast.DefaultHub()))
	m.RegisterChannel("slack", NewSlackChannel(""))
	m.RegisterChannel("sms", NewSMSChannel(nil))

	return m
}

// RegisterChannel registers or overrides a delivery channel by name.
func (m *Manager) RegisterChannel(name string, channel Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[name] = channel

	if dbCh, ok := channel.(*DatabaseChannel); ok {
		m.databaseStore = dbCh.Store()
	}
}

// GetChannel retrieves a registered channel.
func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[name]
	return ch, ok
}

// DatabaseStore returns the active in-app notifications store.
func (m *Manager) DatabaseStore() DatabaseStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.databaseStore
}

// Send dispatches a notification to one or more recipients.
// If the notification implements ShouldQueue and returns true, it is enqueued asynchronously.
func (m *Manager) Send(ctx context.Context, notifiables []Notifiable, notification Notification) error {
	if q, ok := notification.(ShouldQueue); ok && q.ShouldQueue() {
		return m.SendAsync(ctx, notifiables, notification)
	}
	return m.SendNow(ctx, notifiables, notification)
}

// SendNow delivers the notification synchronously across all requested channels.
func (m *Manager) SendNow(ctx context.Context, notifiables []Notifiable, notification Notification) error {
	for _, notifiable := range notifiables {
		channels := notification.Via(notifiable)
		for _, channelName := range channels {
			ch, exists := m.GetChannel(channelName)
			if !exists {
				return fmt.Errorf("notification channel [%s] is not registered", channelName)
			}

			if err := ch.Send(ctx, notifiable, notification); err != nil {
				slog.Error("Failed delivering notification",
					"channel", channelName,
					"notification", fmt.Sprintf("%T", notification),
					"error", err,
				)
				return fmt.Errorf("failed delivering notification via [%s]: %w", channelName, err)
			}
		}
	}
	return nil
}

// SendAsync dispatches the notification delivery to a background goroutine worker.
func (m *Manager) SendAsync(ctx context.Context, notifiables []Notifiable, notification Notification) error {
	job := &NotificationJob{
		manager:      m,
		notifiables:  notifiables,
		notification: notification,
	}

	return m.queuePool.Push(job)
}

// NotificationJob wraps notification execution for asynchronous queue workers.
type NotificationJob struct {
	manager      *Manager
	notifiables  []Notifiable
	notification Notification
}

func (j *NotificationJob) Handle(ctx context.Context) error {
	return j.manager.SendNow(ctx, j.notifiables, j.notification)
}

// Global default notification manager
var defaultManager = NewManager()

// DefaultManager returns the global notification manager instance.
func GetDefaultManager() *Manager {
	return defaultManager
}

// RegisterChannel registers a delivery channel on the default manager.
func RegisterChannel(name string, channel Channel) {
	defaultManager.RegisterChannel(name, channel)
}

// Send dispatches a notification using the global manager.
func Send(ctx context.Context, notifiables []Notifiable, notification Notification) error {
	return defaultManager.Send(ctx, notifiables, notification)
}

// SendOne dispatches a notification to a single recipient.
func SendOne(ctx context.Context, notifiable Notifiable, notification Notification) error {
	return defaultManager.Send(ctx, []Notifiable{notifiable}, notification)
}

// SendNow delivers the notification immediately and synchronously.
func SendNow(ctx context.Context, notifiables []Notifiable, notification Notification) error {
	return defaultManager.SendNow(ctx, notifiables, notification)
}

// SendNowOne delivers the notification immediately to a single recipient.
func SendNowOne(ctx context.Context, notifiable Notifiable, notification Notification) error {
	return defaultManager.SendNow(ctx, []Notifiable{notifiable}, notification)
}

// GetDatabaseStore returns the in-app notification repository from the default manager.
func GetDatabaseStore() DatabaseStore {
	return defaultManager.DatabaseStore()
}
