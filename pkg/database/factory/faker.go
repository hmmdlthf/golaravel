package factory

import (
	"crypto/rand"
	"fmt"
	"math"
	mrand "math/rand"
	"strings"
	"sync"
	"time"
)

// Faker generates realistic fake dummy data for models, seeders, and tests.
type Faker struct {
	mu  sync.Mutex
	rng *mrand.Rand
}

// NewFaker creates a new Faker generator. If a seed is passed, generation is deterministic.
func NewFaker(seed ...int64) *Faker {
	s := time.Now().UnixNano()
	if len(seed) > 0 {
		s = seed[0]
	}
	return &Faker{
		rng: mrand.New(mrand.NewSource(s)),
	}
}

// Default global faker instance
var defaultFaker = NewFaker()

// Seed re-seeds the generator for deterministic testing.
func (f *Faker) Seed(seed int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rng = mrand.New(mrand.NewSource(seed))
}

// Global Seed function
func Seed(seed int64) {
	defaultFaker.Seed(seed)
}

// IntBetween returns a random integer between min and max inclusive.
func (f *Faker) IntBetween(min, max int) int {
	if min >= max {
		return min
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return min + f.rng.Intn(max-min+1)
}

// Int is an alias for IntBetween.
func (f *Faker) Int(min, max int) int {
	return f.IntBetween(min, max)
}

// FloatBetween returns a random float64 between min and max rounded to 2 decimals.
func (f *Faker) FloatBetween(min, max float64, decimals ...int) float64 {
	if min >= max {
		return min
	}
	f.mu.Lock()
	val := min + f.rng.Float64()*(max-min)
	f.mu.Unlock()

	dec := 2
	if len(decimals) > 0 && decimals[0] >= 0 {
		dec = decimals[0]
	}
	multiplier := math.Pow(10, float64(dec))
	return math.Round(val*multiplier) / multiplier
}

// Float is an alias for FloatBetween.
func (f *Faker) Float(min, max float64, decimals ...int) float64 {
	return f.FloatBetween(min, max, decimals...)
}

// Bool returns a random boolean value.
func (f *Faker) Bool() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rng.Intn(2) == 1
}

// Digits generates a string of numeric digits of specified length.
func (f *Faker) Digits(length int) string {
	if length <= 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < length; i++ {
		sb.WriteString(strconvItoa(f.IntBetween(0, 9)))
	}
	return sb.String()
}

// UUID generates a random UUID v4 string.
func (f *Faker) UUID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // Version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
}

// Hex returns random hexadecimal characters of the given length.
func (f *Faker) Hex(length int) string {
	const chars = "0123456789abcdef"
	var sb strings.Builder
	for i := 0; i < length; i++ {
		sb.WriteByte(chars[f.IntBetween(0, len(chars)-1)])
	}
	return sb.String()
}

// Names & People

var firstNames = []string{
	"James", "Mary", "Robert", "Patricia", "John", "Jennifer", "Michael", "Linda",
	"David", "Elizabeth", "William", "Barbara", "Richard", "Susan", "Joseph", "Jessica",
	"Thomas", "Sarah", "Charles", "Karen", "Christopher", "Nancy", "Daniel", "Lisa",
	"Matthew", "Betty", "Anthony", "Margaret", "Mark", "Sandra", "Donald", "Ashley",
	"Steven", "Kimberly", "Paul", "Emily", "Andrew", "Donna", "Joshua", "Michelle",
	"Kenneth", "Dorothy", "Kevin", "Carol", "Brian", "Amanda", "George", "Melissa",
	"Edward", "Deborah", "Ronald", "Stephanie", "Timothy", "Rebecca", "Jason", "Sharon",
	"Jeffrey", "Laura", "Ryan", "Cynthia", "Jacob", "Kathleen", "Gary", "Amy",
	"Nicholas", "Shirley", "Eric", "Angela", "Jonathan", "Helen", "Stephen", "Anna",
	"Larry", "Brenda", "Justin", "Pamela", "Scott", "Nicole", "Brandon", "Emma",
	"Benjamin", "Samantha", "Samuel", "Katherine", "Gregory", "Christine", "Alexander", "Debra",
	"Frank", "Rachel", "Patrick", "Catherine", "Raymond", "Carolyn", "Jack", "Janet",
	"Dennis", "Ruth", "Jerry", "Maria", "Tyler", "Heather", "Aaron", "Diane",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis",
	"Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas",
	"Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson", "White",
	"Harris", "Sanchez", "Clark", "Ramirez", "Lewis", "Robinson", "Walker", "Young",
	"Allen", "King", "Wright", "Scott", "Torres", "Nguyen", "Hill", "Flores",
	"Green", "Adams", "Nelson", "Baker", "Hall", "Rivera", "Campbell", "Mitchell",
	"Carter", "Roberts", "Gomez", "Phillips", "Evans", "Turner", "Diaz", "Parker",
	"Cruz", "Edwards", "Collins", "Reyes", "Stewart", "Morris", "Morales", "Murphy",
	"Cook", "Rogers", "Gutierrez", "Ortiz", "Morgan", "Cooper", "Peterson", "Bailey",
	"Reed", "Kelly", "Howard", "Ramos", "Kim", "Cox", "Ward", "Richardson",
	"Watson", "Brooks", "Chavez", "Wood", "James", "Bennett", "Gray", "Mendoza",
	"Ruiz", "Hughes", "Price", "Alvarez", "Castillo", "Sanders", "Patel", "Myers",
}

// FirstName returns a random first name.
func (f *Faker) FirstName() string {
	return randomFromSlice(f, firstNames)
}

// LastName returns a random last name.
func (f *Faker) LastName() string {
	return randomFromSlice(f, lastNames)
}

// Name returns a random full name (First + Last).
func (f *Faker) Name() string {
	return f.FirstName() + " " + f.LastName()
}

// Username generates a sanitized lowercase username.
func (f *Faker) Username() string {
	first := strings.ToLower(f.FirstName())
	last := strings.ToLower(f.LastName())
	num := f.IntBetween(1, 999)
	return fmt.Sprintf("%s_%s%d", first, last, num)
}

// Internet & Contact

var domainExtensions = []string{"com", "org", "net", "io", "dev", "app", "tech", "co"}

var domainNames = []string{
	"example", "acme", "starlight", "nexus", "vertex", "horizon", "cloudware",
	"cyberdyne", "initech", "umbrella", "wayne", "stark", "hooli", "piedpiper",
}

// DomainName generates a realistic domain name (e.g. "acme.io").
func (f *Faker) DomainName() string {
	domain := randomFromSlice(f, domainNames)
	ext := randomFromSlice(f, domainExtensions)
	return fmt.Sprintf("%s.%s", domain, ext)
}

// Email generates a realistic email address.
func (f *Faker) Email(customDomain ...string) string {
	domain := f.DomainName()
	if len(customDomain) > 0 && customDomain[0] != "" {
		domain = customDomain[0]
	}
	user := strings.ToLower(f.FirstName()) + "." + strings.ToLower(f.LastName()) + strconvItoa(f.IntBetween(1, 99))
	return fmt.Sprintf("%s@%s", user, domain)
}

// SafeEmail generates an email with example.com, example.org, or example.net.
func (f *Faker) SafeEmail() string {
	safeDomains := []string{"example.com", "example.org", "example.net"}
	return f.Email(randomFromSlice(f, safeDomains))
}

// URL generates a realistic web URL.
func (f *Faker) URL() string {
	return fmt.Sprintf("https://www.%s", f.DomainName())
}

// IPv4 generates a random IPv4 address.
func (f *Faker) IPv4() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		f.IntBetween(1, 255),
		f.IntBetween(0, 255),
		f.IntBetween(0, 255),
		f.IntBetween(1, 254),
	)
}

// IPv6 generates a random IPv6 address.
func (f *Faker) IPv6() string {
	return fmt.Sprintf("%x:%x:%x:%x:%x:%x:%x:%x",
		f.IntBetween(0, 0xffff), f.IntBetween(0, 0xffff),
		f.IntBetween(0, 0xffff), f.IntBetween(0, 0xffff),
		f.IntBetween(0, 0xffff), f.IntBetween(0, 0xffff),
		f.IntBetween(0, 0xffff), f.IntBetween(0, 0xffff),
	)
}

// PhoneNumber returns a formatted 10-digit phone number.
func (f *Faker) PhoneNumber() string {
	return fmt.Sprintf("+1 (%d) %d-%04d",
		f.IntBetween(200, 999),
		f.IntBetween(200, 999),
		f.IntBetween(1, 9999),
	)
}

// Text & Content

var loremWords = []string{
	"alias", "consequatur", "aut", "perferendis", "sit", "voluptatem",
	"accusantium", "doloremque", "aperiam", "eaque", "ipsa", "quae", "ab",
	"illo", "inventore", "veritatis", "et", "quasi", "architecto",
	"beatae", "vitae", "dicta", "sunt", "explicabo", "aspernatur", "aut",
	"odit", "aut", "fugit", "sed", "quia", "consequuntur", "magni",
	"dolores", "eos", "qui", "ratione", "voluptatem", "sequi", "nesciunt",
	"neque", "dolorem", "ipsum", "quia", "dolor", "sit", "amet",
	"consectetur", "adipisci", "velit", "sed", "quia", "non", "numquam",
	"eius", "modi", "tempora", "incidunt", "ut", "labore", "et",
	"dolore", "magnam", "aliquam", "quaerat", "voluptatem", "ut", "enim",
	"ad", "minima", "veniam", "quis", "nostrum", "exercitationem", "ullam",
	"corporis", "nemo", "enim", "ipsam", "voluptatem", "quia", "voluptas",
	"sit", "suscipit", "laboriosam", "nisi", "ut", "aliquid", "ex",
	"ea", "commodi", "consequatur", "quis", "autem", "vel", "eum",
	"iure", "reprehenderit", "qui", "in", "ea", "voluptate", "velit",
	"esse", "quam", "nihil", "molestiae", "et", "iusto", "odio",
	"dignissimos", "ducimus", "qui", "blanditiis", "praesentium", "laudantium",
}

// Word returns a random lorem ipsum word.
func (f *Faker) Word() string {
	return randomFromSlice(f, loremWords)
}

// Words returns a slice of random words.
func (f *Faker) Words(count int) []string {
	if count <= 0 {
		count = 3
	}
	res := make([]string, count)
	for i := 0; i < count; i++ {
		res[i] = f.Word()
	}
	return res
}

// Sentence returns a capitalized sentence ending with a period.
func (f *Faker) Sentence(wordCount ...int) string {
	count := 6
	if len(wordCount) > 0 && wordCount[0] > 0 {
		count = wordCount[0]
	}
	words := f.Words(count)
	words[0] = strings.Title(words[0])
	return strings.Join(words, " ") + "."
}

// Sentences returns multiple sentences.
func (f *Faker) Sentences(count int) []string {
	if count <= 0 {
		count = 3
	}
	res := make([]string, count)
	for i := 0; i < count; i++ {
		res[i] = f.Sentence()
	}
	return res
}

// Paragraph returns a cohesive paragraph composed of sentences.
func (f *Faker) Paragraph(sentenceCount ...int) string {
	count := 4
	if len(sentenceCount) > 0 && sentenceCount[0] > 0 {
		count = sentenceCount[0]
	}
	return strings.Join(f.Sentences(count), " ")
}

// Paragraphs returns multiple paragraphs.
func (f *Faker) Paragraphs(count int) []string {
	if count <= 0 {
		count = 3
	}
	res := make([]string, count)
	for i := 0; i < count; i++ {
		res[i] = f.Paragraph()
	}
	return res
}

// Title returns a capitalized title of words.
func (f *Faker) Title(wordCount ...int) string {
	count := 4
	if len(wordCount) > 0 && wordCount[0] > 0 {
		count = wordCount[0]
	}
	words := f.Words(count)
	for i, w := range words {
		words[i] = strings.Title(w)
	}
	return strings.Join(words, " ")
}

// Business & Commerce

var companyPrefixes = []string{
	"Global", "Apex", "Starlight", "Dynamic", "Strategic", "United", "Prime", "Precision",
	"National", "Vanguard", "Innovative", "Pacific", "Atlantic", "Summit", "Quantum",
}

var companySuffixes = []string{
	"Inc", "LLC", "Corp", "Group", "Solutions", "Technologies", "Holdings", "Ventures", "Labs", "Enterprises",
}

var jobTitles = []string{
	"Chief Executive Officer", "Chief Technology Officer", "Software Engineer",
	"Senior Backend Developer", "Product Manager", "DevOps Architect", "Security Engineer",
	"Data Scientist", "UI/UX Designer", "VP of Engineering", "Solutions Architect",
	"Site Reliability Engineer", "Full Stack Developer", "Database Administrator",
}

// Company generates a realistic enterprise company name.
func (f *Faker) Company() string {
	return randomFromSlice(f, companyPrefixes) + " " + randomFromSlice(f, domainNames) + " " + randomFromSlice(f, companySuffixes)
}

// JobTitle returns a realistic professional job title.
func (f *Faker) JobTitle() string {
	return randomFromSlice(f, jobTitles)
}

// Price returns a monetary amount between min and max rounded to 2 decimal places.
func (f *Faker) Price(min, max float64) float64 {
	return f.FloatBetween(min, max, 2)
}

// CurrencyCode returns an ISO 4217 currency code.
func (f *Faker) CurrencyCode() string {
	currencies := []string{"USD", "EUR", "GBP", "JPY", "CAD", "AUD", "CHF"}
	return randomFromSlice(f, currencies)
}

// CreditCardNumber generates a realistic Luhn-compliant or masked 16-digit card number.
func (f *Faker) CreditCardNumber() string {
	return fmt.Sprintf("4%03d-%04d-%04d-%04d",
		f.IntBetween(100, 999),
		f.IntBetween(1000, 9999),
		f.IntBetween(1000, 9999),
		f.IntBetween(1000, 9999),
	)
}

// Location & Address

var streetNames = []string{
	"Main St", "Oak Ave", "Maple Dr", "Broadway", "Pine St", "Cedar Ln",
	"Park Ave", "Washington St", "Lakeview Blvd", "Sunset Rd", "Market St",
}

var cities = []string{
	"San Francisco", "New York", "Austin", "Seattle", "Chicago", "Boston",
	"Denver", "Miami", "Atlanta", "Los Angeles", "Portland", "Dallas",
}

var countries = []string{
	"United States", "United Kingdom", "Germany", "France", "Canada",
	"Australia", "Japan", "Netherlands", "Sweden", "Switzerland",
}

// StreetAddress returns a street address (number + street).
func (f *Faker) StreetAddress() string {
	return fmt.Sprintf("%d %s", f.IntBetween(100, 9999), randomFromSlice(f, streetNames))
}

// City returns a major city name.
func (f *Faker) City() string {
	return randomFromSlice(f, cities)
}

// Country returns a country name.
func (f *Faker) Country() string {
	return randomFromSlice(f, countries)
}

// ZipCode returns a 5-digit postal code.
func (f *Faker) ZipCode() string {
	return fmt.Sprintf("%05d", f.IntBetween(10000, 99999))
}

// Dates & Time

// PastDate returns a time.Time in the past within maxYears.
func (f *Faker) PastDate(years ...int) time.Time {
	maxYears := 2
	if len(years) > 0 && years[0] > 0 {
		maxYears = years[0]
	}
	days := f.IntBetween(1, maxYears*365)
	return time.Now().AddDate(0, 0, -days)
}

// FutureDate returns a time.Time in the future within maxYears.
func (f *Faker) FutureDate(years ...int) time.Time {
	maxYears := 2
	if len(years) > 0 && years[0] > 0 {
		maxYears = years[0]
	}
	days := f.IntBetween(1, maxYears*365)
	return time.Now().AddDate(0, 0, days)
}

// DateTimeBetween returns a timestamp between from and to.
func (f *Faker) DateTimeBetween(from, to time.Time) time.Time {
	if from.After(to) {
		from, to = to, from
	}
	diff := to.Unix() - from.Unix()
	if diff <= 0 {
		return from
	}
	sec := f.IntBetween(0, int(diff))
	return from.Add(time.Duration(sec) * time.Second)
}

// Generic Helpers

func randomFromSlice[T any](f *Faker, slice []T) T {
	if len(slice) == 0 {
		var zero T
		return zero
	}
	f.mu.Lock()
	idx := f.rng.Intn(len(slice))
	f.mu.Unlock()
	return slice[idx]
}

// RandomElement selects a random item from a slice.
func RandomElement[T any](f *Faker, slice []T) T {
	return randomFromSlice(f, slice)
}

// Shuffle randomizes the order of a slice in-place and returns it.
func Shuffle[T any](f *Faker, slice []T) []T {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rng.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})
	return slice
}

func strconvItoa(n int) string {
	return fmt.Sprintf("%d", n)
}
