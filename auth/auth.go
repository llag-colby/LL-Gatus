// Package auth provides local user accounts, password hashing, sessions and
// role checks for the dashboard.
//
// Storage is a sidecar SQLite file, for the same reasons the history package
// keeps its own: the Gatus store deletes rows that cascade from the endpoints
// table on every startup, and it runs with a single connection that a login
// query has no business competing for.
//
// The permission model is deliberately small. Anonymous callers may read; a
// session is what unlocks anything that changes state. Roles are ordered, so a
// check is a comparison rather than a set membership test:
//
//	viewer   < operator < admin
//
// Collector pushes do NOT pass through here. They authenticate with the bearer
// token configured on their external endpoint, which is a separate mechanism
// with a separate threat model: a token identifies a machine, a session
// identifies a person.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/logr"
	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite"
)

// Role is an ordered privilege level. Higher is more privileged.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// Valid reports whether r is a role this system knows.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// AtLeast reports whether r is at least as privileged as minimum.
func (r Role) AtLeast(minimum Role) bool { return roleRank[r] >= roleRank[minimum] }

// User is an account. PasswordHash is never serialized: the json tag is "-" so
// a handler cannot leak it by marshalling a User straight back to the client.
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	Role         Role       `json:"role"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
}

// Session is a logged-in browser.
type Session struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
}

const (
	// SessionCookieName is the cookie the browser carries. Prefixed to make it
	// obvious in devtools which product owns it.
	SessionCookieName = "gatus_session"
	// sessionTTL is how long a login lasts. Long enough that a tech is not
	// re-authenticating through a shift, short enough that a forgotten session on
	// a shared machine expires on its own.
	sessionTTL = 12 * time.Hour
	// bcryptCost is deliberately above the library default: this is a small
	// user base on a machine that is not doing anything else at login time.
	bcryptCost = 12
	// minPasswordLength is a floor, not a policy. Complexity rules push people
	// towards predictable substitutions; length is what actually helps.
	minPasswordLength = 10
)

var (
	// ErrNotConfigured means Open was never called or failed. Every exported
	// function returns it rather than panicking, so a broken auth database
	// degrades to "nobody can log in" instead of taking the dashboard down.
	ErrNotConfigured = errors.New("auth is not configured")
	// ErrInvalidCredentials is returned for both an unknown username and a wrong
	// password, on purpose: distinguishing them tells an attacker which usernames
	// exist.
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserExists         = errors.New("a user with that username already exists")
	ErrUserNotFound       = errors.New("user not found")
	ErrWeakPassword       = fmt.Errorf("password must be at least %d characters", minPasswordLength)
	ErrLastAdmin          = errors.New("this is the last admin; promote another user first")
)

var (
	mu sync.RWMutex
	db *sql.DB
)

// Open opens (creating it if necessary) the auth database at path and creates
// the schema. Safe to call once from main; a returned error means logins are
// unavailable, not that the application cannot run.
func Open(path string) error {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		return nil
	}
	handle, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	// One connection, matching how the main store treats SQLite. Auth queries are
	// tiny and infrequent, so there is nothing to gain from concurrency here and
	// a single writer avoids "database is locked" entirely.
	handle.SetMaxOpenConns(1)
	if err = handle.Ping(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("pinging %s: %w", path, err)
	}
	if err = createSchema(handle); err != nil {
		_ = handle.Close()
		return fmt.Errorf("creating auth schema: %w", err)
	}
	db = handle
	go pruneExpiredSessionsForever()
	return nil
}

func createSchema(handle *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			user_id       INTEGER PRIMARY KEY,
			username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL,
			created_at    INTEGER NOT NULL,
			last_login_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id)`,
		`CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at)`,
	}
	for _, statement := range statements {
		if _, err := handle.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func get() *sql.DB {
	mu.RLock()
	defer mu.RUnlock()
	return db
}

// Enabled reports whether the auth database is available. When it is not, the
// UI shows no login and every control stays anonymous, which is the pre-auth
// behaviour rather than a lockout.
func Enabled() bool { return get() != nil }

// --- passwords -------------------------------------------------------------

func hashPassword(plaintext string) (string, error) {
	if len([]rune(plaintext)) < minPasswordLength {
		return "", ErrWeakPassword
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

// --- users -----------------------------------------------------------------

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// CreateUser adds an account. Usernames are compared case-insensitively so
// "Colby" and "colby" cannot both exist.
func CreateUser(username, password string, role Role) (*User, error) {
	handle := get()
	if handle == nil {
		return nil, ErrNotConfigured
	}
	username = normalizeUsername(username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	if !role.Valid() {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	hashed, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result, err := handle.Exec(
		`INSERT INTO users (username, password_hash, role, created_at) VALUES ($1, $2, $3, $4)`,
		username, hashed, string(role), now.Unix(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := result.LastInsertId()
	return &User{ID: id, Username: username, Role: role, CreatedAt: now}, nil
}

// GetUser looks an account up by id.
func GetUser(id int64) (*User, error) {
	handle := get()
	if handle == nil {
		return nil, ErrNotConfigured
	}
	return scanUser(handle.QueryRow(
		`SELECT user_id, username, password_hash, role, created_at, last_login_at FROM users WHERE user_id = $1`, id))
}

// ListUsers returns every account, oldest first. Password hashes are loaded but
// never serialized (see the User json tags).
func ListUsers() ([]User, error) {
	handle := get()
	if handle == nil {
		return nil, ErrNotConfigured
	}
	rows, err := handle.Query(
		`SELECT user_id, username, password_hash, role, created_at, last_login_at FROM users ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *user)
	}
	return users, rows.Err()
}

type scannable interface{ Scan(dest ...any) error }

func scanUser(row scannable) (*User, error) {
	var user User
	var role string
	var createdAt int64
	var lastLogin sql.NullInt64
	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &role, &createdAt, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	user.Role = Role(role)
	user.CreatedAt = time.Unix(createdAt, 0)
	if lastLogin.Valid {
		when := time.Unix(lastLogin.Int64, 0)
		user.LastLoginAt = &when
	}
	return &user, nil
}

// SetPassword replaces a user's password.
func SetPassword(id int64, password string) error {
	handle := get()
	if handle == nil {
		return ErrNotConfigured
	}
	hashed, err := hashPassword(password)
	if err != nil {
		return err
	}
	result, err := handle.Exec(`UPDATE users SET password_hash = $1 WHERE user_id = $2`, hashed, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrUserNotFound
	}
	// Changing a password invalidates that user's other sessions. A password
	// change is usually a response to it having been exposed, so leaving old
	// sessions alive would defeat the point.
	_, _ = handle.Exec(`DELETE FROM sessions WHERE user_id = $1`, id)
	return nil
}

// SetRole changes a user's role, refusing to demote the last remaining admin.
func SetRole(id int64, role Role) error {
	handle := get()
	if handle == nil {
		return ErrNotConfigured
	}
	if !role.Valid() {
		return fmt.Errorf("unknown role %q", role)
	}
	if role != RoleAdmin {
		if err := guardLastAdmin(handle, id); err != nil {
			return err
		}
	}
	result, err := handle.Exec(`UPDATE users SET role = $1 WHERE user_id = $2`, string(role), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes an account and its sessions, refusing to remove the last
// admin so the system can never be locked out of its own settings.
func DeleteUser(id int64) error {
	handle := get()
	if handle == nil {
		return ErrNotConfigured
	}
	if err := guardLastAdmin(handle, id); err != nil {
		return err
	}
	result, err := handle.Exec(`DELETE FROM users WHERE user_id = $1`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrUserNotFound
	}
	_, _ = handle.Exec(`DELETE FROM sessions WHERE user_id = $1`, id)
	return nil
}

// guardLastAdmin returns ErrLastAdmin when id is the only remaining admin.
func guardLastAdmin(handle *sql.DB, id int64) error {
	var isAdmin bool
	if err := handle.QueryRow(
		`SELECT role = 'admin' FROM users WHERE user_id = $1`, id).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return err
	}
	if !isAdmin {
		return nil
	}
	var admins int
	if err := handle.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		return err
	}
	if admins <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// CountUsers reports how many accounts exist, so the UI can tell "auth is off"
// from "auth is on and nobody has signed in yet".
func CountUsers() (int, error) {
	handle := get()
	if handle == nil {
		return 0, ErrNotConfigured
	}
	var count int
	err := handle.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// --- login and sessions ----------------------------------------------------

// Login verifies credentials and issues a session.
//
// A failed lookup still runs a bcrypt comparison against a dummy hash so that
// an unknown username and a wrong password take the same time. Without that,
// response timing enumerates valid usernames.
func Login(username, password string) (*Session, *User, error) {
	handle := get()
	if handle == nil {
		return nil, nil, ErrNotConfigured
	}
	user, err := scanUser(handle.QueryRow(
		`SELECT user_id, username, password_hash, role, created_at, last_login_at FROM users WHERE username = $1`,
		normalizeUsername(username)))
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Cost-matched dummy comparison. The hash is of a value nothing can
			// present, so this can never succeed.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return nil, nil, ErrInvalidCredentials
		}
		return nil, nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, nil, ErrInvalidCredentials
	}
	session, err := newSession(handle, user.ID)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	_, _ = handle.Exec(`UPDATE users SET last_login_at = $1 WHERE user_id = $2`, now.Unix(), user.ID)
	user.LastLoginAt = &now
	logr.Infof("[auth.Login] %s signed in as %s", user.Username, user.Role)
	return session, user, nil
}

// dummyHash is a bcrypt hash at the same cost as a real one, used to keep the
// unknown-username path as expensive as the wrong-password path.
var dummyHash = func() []byte {
	hashed, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)
	if err != nil {
		// Cannot happen with a valid cost, and there is nothing useful to do
		// about it at init time other than fail closed on the timing defence.
		return []byte("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinva")
	}
	return hashed
}()

func newSession(handle *sql.DB, userID int64) (*Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	session := &Session{
		Token:     hex.EncodeToString(raw),
		UserID:    userID,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
	_, err := handle.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		session.Token, session.UserID, session.ExpiresAt.Unix())
	if err != nil {
		return nil, err
	}
	return session, nil
}

// UserForSession resolves a session token to its user, or nil when the token is
// unknown or expired. A nil user is not an error: it is an anonymous caller.
func UserForSession(token string) *User {
	handle := get()
	if handle == nil || token == "" {
		return nil
	}
	var userID int64
	var expiresAt int64
	var stored string
	err := handle.QueryRow(
		`SELECT token, user_id, expires_at FROM sessions WHERE token = $1`, token).Scan(&stored, &userID, &expiresAt)
	if err != nil {
		return nil
	}
	// Constant-time compare even though the lookup was by primary key: it costs
	// nothing and keeps the comparison honest if this is ever refactored to scan.
	if subtle.ConstantTimeCompare([]byte(stored), []byte(token)) != 1 {
		return nil
	}
	if time.Now().After(time.Unix(expiresAt, 0)) {
		_, _ = handle.Exec(`DELETE FROM sessions WHERE token = $1`, token)
		return nil
	}
	user, err := GetUser(userID)
	if err != nil {
		return nil
	}
	return user
}

// Logout destroys one session. Deleting an unknown token is not an error.
func Logout(token string) {
	handle := get()
	if handle == nil || token == "" {
		return
	}
	_, _ = handle.Exec(`DELETE FROM sessions WHERE token = $1`, token)
}

// SessionTTL exposes the lifetime so handlers can set a matching cookie MaxAge.
func SessionTTL() time.Duration { return sessionTTL }

func pruneExpiredSessionsForever() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		handle := get()
		if handle == nil {
			return
		}
		if _, err := handle.Exec(`DELETE FROM sessions WHERE expires_at < $1`, time.Now().Unix()); err != nil {
			logr.Errorf("[auth.pruneExpiredSessions] %s", err.Error())
		}
	}
}

// --- bootstrap -------------------------------------------------------------

// EnsureAdmin creates the first administrator when the user table is empty.
//
// GATUS_ADMIN_USER and GATUS_ADMIN_PASSWORD seed it. With no environment set,
// an "admin" account is created with a generated password logged once at
// startup, because the alternative, an open setup page reachable by anyone on
// the network, is worse.
func EnsureAdmin() {
	handle := get()
	if handle == nil {
		return
	}
	count, err := CountUsers()
	if err != nil {
		logr.Errorf("[auth.EnsureAdmin] could not count users: %s", err.Error())
		return
	}
	if count > 0 {
		return
	}
	username := strings.TrimSpace(os.Getenv("GATUS_ADMIN_USER"))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("GATUS_ADMIN_PASSWORD")
	generated := false
	if len([]rune(password)) < minPasswordLength {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			logr.Errorf("[auth.EnsureAdmin] could not generate a password: %s", err.Error())
			return
		}
		password = hex.EncodeToString(raw)
		generated = true
	}
	if _, err := CreateUser(username, password, RoleAdmin); err != nil {
		logr.Errorf("[auth.EnsureAdmin] could not create the first admin: %s", err.Error())
		return
	}
	if generated {
		logr.Warnf("[auth.EnsureAdmin] Created first admin %q with generated password: %s", username, password)
		logr.Warnf("[auth.EnsureAdmin] Sign in and change it. Set GATUS_ADMIN_PASSWORD to choose your own.")
	} else {
		logr.Infof("[auth.EnsureAdmin] Created first admin %q from GATUS_ADMIN_PASSWORD", username)
	}
}
