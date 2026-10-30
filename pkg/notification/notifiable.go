package notification

// Notifiable defines the contract for any entity (e.g. User, Team, Customer)
// that can receive notifications across various channels.
type Notifiable interface {
	RouteNotificationFor(channel string) string
}

// AnonymousNotifiable allows sending notifications directly to custom destinations
// (e.g. on-demand emails, phone numbers, or webhook URLs) without an underlying model.
type AnonymousNotifiable struct {
	routes map[string]string
}

// NewAnonymousNotifiable creates an on-demand recipient for ad-hoc notifications.
func NewAnonymousNotifiable() *AnonymousNotifiable {
	return &AnonymousNotifiable{
		routes: make(map[string]string),
	}
}

// Route sets the destination route for a given channel.
func (a *AnonymousNotifiable) Route(channel, destination string) *AnonymousNotifiable {
	a.routes[channel] = destination
	return a
}

// RouteNotificationFor returns the routing address for the specified channel.
func (a *AnonymousNotifiable) RouteNotificationFor(channel string) string {
	return a.routes[channel]
}

// OnDemand creates an AnonymousNotifiable with a single channel route.
// Example: notification.OnDemand("mail", "finance@enterprise.com")
func OnDemand(channel, destination string) *AnonymousNotifiable {
	return NewAnonymousNotifiable().Route(channel, destination)
}
