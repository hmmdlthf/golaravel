package factory_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/database/factory"
)

type UserDef struct {
	ID        int
	Name      string
	Email     string
	Role      string
	IsActive  bool
	CreatedAt time.Time
}

type MockDBClient struct {
	SavedUsers []*UserDef
}

func TestFaker_CoreAndCategories(t *testing.T) {
	f := factory.NewFaker(42)

	// Names
	if f.FirstName() == "" || f.LastName() == "" || f.Name() == "" || f.Username() == "" {
		t.Errorf("names should not be empty")
	}

	// Internet
	if !strings.Contains(f.Email(), "@") {
		t.Errorf("email should contain @: %s", f.Email())
	}
	if !strings.HasPrefix(f.URL(), "https://") {
		t.Errorf("url should start with https://: %s", f.URL())
	}
	if len(strings.Split(f.IPv4(), ".")) != 4 {
		t.Errorf("ipv4 should have 4 octets: %s", f.IPv4())
	}

	// Numbers & UUID
	uuid := f.UUID()
	if len(uuid) != 36 {
		t.Errorf("expected 36-char UUID, got %s", uuid)
	}
	if n := f.IntBetween(10, 20); n < 10 || n > 20 {
		t.Errorf("integer out of range: %d", n)
	}
	if p := f.Price(10.0, 50.0); p < 10.0 || p > 50.0 {
		t.Errorf("price out of range: %f", p)
	}

	// Text
	if len(f.Words(5)) != 5 {
		t.Errorf("expected 5 words")
	}
	if !strings.HasSuffix(f.Sentence(), ".") {
		t.Errorf("sentence should end with period")
	}
	if len(f.Paragraphs(2)) != 2 {
		t.Errorf("expected 2 paragraphs")
	}

	// Business & Address
	if f.Company() == "" || f.JobTitle() == "" || f.CreditCardNumber() == "" {
		t.Errorf("business fields should not be empty")
	}
	if f.Country() == "" || f.City() == "" || f.StreetAddress() == "" {
		t.Errorf("address fields should not be empty")
	}

	// Dates
	now := time.Now()
	past := f.PastDate(1)
	if !past.Before(now) {
		t.Errorf("past date must be before now")
	}
	future := f.FutureDate(1)
	if !future.After(now) {
		t.Errorf("future date must be after now")
	}
}

func TestFaker_DeterministicSeeding(t *testing.T) {
	f1 := factory.NewFaker(1337)
	f2 := factory.NewFaker(1337)

	for i := 0; i < 20; i++ {
		name1 := f1.Name()
		name2 := f2.Name()
		if name1 != name2 {
			t.Fatalf("at iteration %d, expected matching names: %s != %s", i, name1, name2)
		}

		email1 := f1.Email()
		email2 := f2.Email()
		if email1 != email2 {
			t.Fatalf("at iteration %d, expected matching emails: %s != %s", i, email1, email2)
		}
	}
}

func TestFactory_MakeOneAndBulk(t *testing.T) {
	userFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{
				Name:      f.Name(),
				Email:     f.Email(),
				Role:      "member",
				IsActive:  true,
				CreatedAt: time.Now(),
			}
		})

	// MakeOne
	one := userFactory.MakeOne()
	if one.Name == "" || one.Email == "" || one.Role != "member" {
		t.Fatalf("unexpected MakeOne result: %+v", one)
	}

	// Bulk Make
	many := userFactory.Count(5).Make()
	if len(many) != 5 {
		t.Fatalf("expected 5 models, got %d", len(many))
	}
	for i, u := range many {
		if u.Name == "" || u.Email == "" {
			t.Errorf("model at index %d has empty fields", i)
		}
	}
}

func TestFactory_StateMutations(t *testing.T) {
	userFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{
				Name:  f.Name(),
				Email: f.Email(),
				Role:  "member",
			}
		})

	adminFactory := userFactory.State(func(m *UserDef) {
		m.Role = "admin"
	})

	admins := adminFactory.Count(3).Make()
	for i, a := range admins {
		if a.Role != "admin" {
			t.Errorf("at index %d expected role admin, got %s", i, a.Role)
		}
	}

	// Verify original factory wasn't modified (immutability)
	orig := userFactory.MakeOne()
	if orig.Role != "member" {
		t.Errorf("expected original factory to keep role 'member', got %s", orig.Role)
	}
}

func TestFactory_Sequence(t *testing.T) {
	userFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{Name: f.Name()}
		}).
		Sequence(func(n int, m *UserDef) {
			m.ID = n + 1
			m.Email = fmt.Sprintf("user%d@example.com", n+1)
		})

	users := userFactory.Count(4).Make()
	for i, u := range users {
		expectedID := i + 1
		expectedEmail := fmt.Sprintf("user%d@example.com", i+1)
		if u.ID != expectedID || u.Email != expectedEmail {
			t.Errorf("at index %d expected ID=%d Email=%s, got ID=%d Email=%s",
				i, expectedID, expectedEmail, u.ID, u.Email)
		}
	}
}

func TestFactory_HooksAndCreate(t *testing.T) {
	ctx := context.Background()
	mockDB := &MockDBClient{}

	var afterMakingTriggered int
	var afterCreatingTriggered int

	userFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{
				Name:  f.Name(),
				Email: f.Email(),
				Role:  "member",
			}
		}).
		AfterMaking(func(m *UserDef) {
			afterMakingTriggered++
		}).
		Creator(func(ctx context.Context, client any, def UserDef) (any, error) {
			c := client.(*MockDBClient)
			entity := &UserDef{
				ID:    len(c.SavedUsers) + 1,
				Name:  def.Name,
				Email: def.Email,
				Role:  def.Role,
			}
			c.SavedUsers = append(c.SavedUsers, entity)
			return entity, nil
		}).
		AfterCreating(func(ctx context.Context, client any, entity any) error {
			afterCreatingTriggered++
			return nil
		})

	// Create 3 records
	created, err := userFactory.Count(3).Create(ctx, mockDB)
	if err != nil {
		t.Fatalf("unexpected Create error: %v", err)
	}

	if len(created) != 3 {
		t.Fatalf("expected 3 created records, got %d", len(created))
	}
	if len(mockDB.SavedUsers) != 3 {
		t.Fatalf("expected 3 saved users in mock db, got %d", len(mockDB.SavedUsers))
	}
	if afterMakingTriggered != 3 {
		t.Errorf("expected afterMaking hook to trigger 3 times, got %d", afterMakingTriggered)
	}
	if afterCreatingTriggered != 3 {
		t.Errorf("expected afterCreating hook to trigger 3 times, got %d", afterCreatingTriggered)
	}

	// CreateOne
	single, err := userFactory.CreateOne(ctx, mockDB)
	if err != nil {
		t.Fatalf("CreateOne error: %v", err)
	}
	u := single.(*UserDef)
	if u.ID != 4 {
		t.Errorf("expected ID 4 for single record, got %d", u.ID)
	}
}

func TestFactory_CreatorErrorHandling(t *testing.T) {
	userFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{}
		}).
		Creator(func(ctx context.Context, client any, def UserDef) (any, error) {
			return nil, errors.New("db connection failure")
		})

	_, err := userFactory.Create(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "db connection failure") {
		t.Fatalf("expected db connection failure error, got: %v", err)
	}
}

func TestFactory_Registry(t *testing.T) {
	factory.ClearRegistry()

	baseFactory := factory.New[UserDef]().
		Definition(func(f *factory.Faker) UserDef {
			return UserDef{Role: "member"}
		})

	factory.Register("user", baseFactory)

	retrieved, err := factory.Get[UserDef]("user")
	if err != nil {
		t.Fatalf("failed retrieving registered factory: %v", err)
	}

	item := retrieved.MakeOne()
	if item.Role != "member" {
		t.Errorf("expected role 'member', got %s", item.Role)
	}

	// Not found
	_, errMissing := factory.Get[UserDef]("missing")
	if errMissing == nil {
		t.Errorf("expected error for missing factory")
	}
}
