package notifications

import (
	"fmt"

	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

// InvoicePaidNotification delivers multi-channel confirmation across mail, in-app database, and broadcast.
type InvoicePaidNotification struct {
	InvoiceNumber string
	CustomerName  string
	Amount        float64
}

// NewInvoicePaidNotification creates a new InvoicePaidNotification instance.
func NewInvoicePaidNotification(num, customer string, amount float64) *InvoicePaidNotification {
	return &InvoicePaidNotification{
		InvoiceNumber: num,
		CustomerName:  customer,
		Amount:        amount,
	}
}

// Via determines which channels the notification should be delivered on.
func (n *InvoicePaidNotification) Via(notifiable notification.Notifiable) []string {
	return []string{"mail", "database", "broadcast"}
}

// ToMail constructs the receipt email.
func (n *InvoicePaidNotification) ToMail(notifiable notification.Notifiable) (*mail.Message, error) {
	return mail.NewMessage().
		Subject(fmt.Sprintf("Payment Receipt: %s", n.InvoiceNumber)).
		HTML(fmt.Sprintf("<h2>Invoice Paid</h2><p>Payment of $%.2f from <strong>%s</strong> was confirmed.</p>", n.Amount, n.CustomerName)), nil
}

// ToDatabase constructs the in-app database alert record.
func (n *InvoicePaidNotification) ToDatabase(notifiable notification.Notifiable) (*notification.DatabaseMessage, error) {
	return &notification.DatabaseMessage{
		Type: "InvoicePaid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"customer_name":  n.CustomerName,
			"amount":         n.Amount,
		},
	}, nil
}

// ToBroadcast constructs the real-time push event for WebSockets and SSE.
func (n *InvoicePaidNotification) ToBroadcast(notifiable notification.Notifiable) (*broadcast.Message, error) {
	return &broadcast.Message{
		Event: "invoice.paid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"customer_name":  n.CustomerName,
			"amount":         n.Amount,
		},
	}, nil
}
