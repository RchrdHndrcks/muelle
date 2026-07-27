package quickcmd

import (
	"strings"
	"testing"
)

// argv joins a command's arguments for readable comparisons.
func argv(c Command) string { return strings.Join(c.Argv, " ") }

// find returns the first command whose label contains substring.
func find(t *testing.T, commands []Command, substring string) Command {
	t.Helper()
	for _, c := range commands {
		if strings.Contains(c.Label, substring) {
			return c
		}
	}
	t.Fatalf("no command labelled %q in %v", substring, labels(commands))
	return Command{}
}

func labels(commands []Command) []string {
	var out []string
	for _, c := range commands {
		out = append(out, c.Label)
	}
	return out
}

// The whole point of the feature: the password the user cannot remember is
// already in the container's environment.
func TestSuggestMySQLUsesRootPasswordFromEnv(t *testing.T) {
	commands := Suggest("mysql:8.0", map[string]string{
		"MYSQL_ROOT_PASSWORD": "s3cret",
		"MYSQL_DATABASE":      "app_db",
	})

	root := find(t, commands, "mysql as root")
	if got := argv(root); got != "mysql -u root -ps3cret app_db" {
		t.Errorf("got %q, want the password attached to -p and the database appended", got)
	}
	if !root.Sensitive {
		t.Error("a command carrying a password should be marked sensitive")
	}
}

// "-p secret" is parsed by the MySQL client as a database name, not a
// password, so the value must be attached to the flag.
func TestSuggestMySQLAttachesPasswordToFlag(t *testing.T) {
	commands := Suggest("mysql:8.0", map[string]string{"MYSQL_ROOT_PASSWORD": "pw"})

	root := find(t, commands, "mysql as root")
	for i, arg := range root.Argv {
		if arg == "-p" {
			t.Fatalf("argument %d is a bare -p; the password must be attached: %v", i, root.Argv)
		}
	}
}

func TestSuggestMySQLWithoutPasswordOmitsFlag(t *testing.T) {
	commands := Suggest("mysql:8.0", nil)

	root := find(t, commands, "mysql as root")
	if got := argv(root); got != "mysql -u root" {
		t.Errorf("got %q, want no password flag when none is set", got)
	}
	if root.Sensitive {
		t.Error("a command with no credential is not sensitive")
	}
}

func TestSuggestMySQLOffersApplicationUser(t *testing.T) {
	commands := Suggest("mysql:8.0", map[string]string{
		"MYSQL_ROOT_PASSWORD": "root-pw",
		"MYSQL_USER":          "app",
		"MYSQL_PASSWORD":      "app-pw",
		"MYSQL_DATABASE":      "app_db",
	})

	user := find(t, commands, "mysql as app")
	if got := argv(user); got != "mysql -u app -papp-pw app_db" {
		t.Errorf("got %q, want the application user's own credentials", got)
	}
}

func TestSuggestMariaDBReadsItsOwnEnvNames(t *testing.T) {
	commands := Suggest("mariadb:11", map[string]string{
		"MARIADB_ROOT_PASSWORD": "mpw",
		"MARIADB_DATABASE":      "blog",
	})

	root := find(t, commands, "mysql as root")
	if got := argv(root); got != "mysql -u root -pmpw blog" {
		t.Errorf("got %q, want MARIADB_* honoured", got)
	}
}

// A *_FILE variable holds a path to a secret, not the secret; using it would
// build a command that fails confusingly.
func TestSuggestMySQLIgnoresSecretFileIndirection(t *testing.T) {
	commands := Suggest("mysql:8.0", map[string]string{
		"MYSQL_ROOT_PASSWORD_FILE": "/run/secrets/db_password",
	})

	root := find(t, commands, "mysql as root")
	if strings.Contains(argv(root), "/run/secrets") {
		t.Errorf("got %q, want the file path left out of the command", argv(root))
	}
}

// Registry prefixes and tags must not defeat image matching.
func TestSuggestMatchesFullyQualifiedImageReference(t *testing.T) {
	commands := Suggest("docker.io/library/mysql:8.0.35-debian", map[string]string{
		"MYSQL_ROOT_PASSWORD": "pw",
	})

	find(t, commands, "mysql as root")
}

func TestSuggestPostgresUsesConfiguredUserAndDatabase(t *testing.T) {
	commands := Suggest("postgres:16-alpine", map[string]string{
		"POSTGRES_USER": "admin",
		"POSTGRES_DB":   "records",
	})

	psql := find(t, commands, "psql as admin")
	if got := argv(psql); got != "psql -U admin -d records" {
		t.Errorf("got %q, want the configured user and database", got)
	}
}

func TestSuggestPostgresDefaultsToPostgresUser(t *testing.T) {
	commands := Suggest("postgres:16", nil)

	psql := find(t, commands, "psql as postgres")
	if got := argv(psql); got != "psql -U postgres -d postgres" {
		t.Errorf("got %q, want the image defaults", got)
	}
}

// psql takes its password from PGPASSWORD, never the command line.
func TestSuggestPostgresKeepsPasswordOffCommandLine(t *testing.T) {
	commands := Suggest("postgres:16", map[string]string{"POSTGRES_PASSWORD": "hunter2"})

	for _, c := range commands {
		if strings.Contains(argv(c), "hunter2") {
			t.Errorf("command %q leaks the password onto the command line", argv(c))
		}
	}
}

func TestSuggestRedisAuthenticatesOnlyWhenConfigured(t *testing.T) {
	withoutAuth := Suggest("redis:7-alpine", nil)
	if got := argv(find(t, withoutAuth, "redis-cli")); got != "redis-cli" {
		t.Errorf("got %q, want a bare redis-cli", got)
	}

	withAuth := Suggest("redis:7-alpine", map[string]string{"REDIS_PASSWORD": "pw"})
	if got := argv(find(t, withAuth, "redis-cli")); got != "redis-cli -a pw" {
		t.Errorf("got %q, want the auth flag", got)
	}
}

func TestSuggestMongoPrefersMongosh(t *testing.T) {
	commands := Suggest("mongo:7", map[string]string{
		"MONGO_INITDB_ROOT_USERNAME": "root",
		"MONGO_INITDB_ROOT_PASSWORD": "pw",
	})

	if got := argv(find(t, commands, "mongosh")); got != "mongosh -u root -p pw" {
		t.Errorf("got %q, want mongosh with credentials", got)
	}
}

// The menu is on screen and screens get shared; a password must never render.
func TestStringMasksAttachedPassword(t *testing.T) {
	c := Command{Argv: []string{"mysql", "-u", "root", "-ps3cret", "db"}, Sensitive: true}

	got := c.String()

	if strings.Contains(got, "s3cret") {
		t.Errorf("got %q, want the password masked", got)
	}
	if !strings.Contains(got, "mysql -u root -p") || !strings.Contains(got, "db") {
		t.Errorf("got %q, want the rest of the command still readable", got)
	}
}

// mongosh and redis-cli take the password as its own argument, which a
// prefix-only check would miss entirely.
func TestStringMasksDetachedPassword(t *testing.T) {
	for _, c := range []Command{
		{Argv: []string{"mongosh", "-u", "root", "-p", "s3cret"}, Sensitive: true},
		{Argv: []string{"redis-cli", "-a", "s3cret"}, Sensitive: true},
	} {
		if got := c.String(); strings.Contains(got, "s3cret") {
			t.Errorf("got %q, want the detached password masked", got)
		}
	}
}

// Mask width must not reveal the password's length.
func TestStringMaskHidesPasswordLength(t *testing.T) {
	short := Command{Argv: []string{"mysql", "-pa"}, Sensitive: true}.String()
	long := Command{Argv: []string{"mysql", "-paaaaaaaaaaaaaaaaaaaa"}, Sensitive: true}.String()

	if len(short) != len(long) {
		t.Errorf("got %q and %q; mask width should not vary with the secret", short, long)
	}
}

func TestStringLeavesPlainCommandsIntact(t *testing.T) {
	c := Command{Argv: []string{"psql", "-U", "postgres", "-d", "postgres"}}

	if got := c.String(); got != "psql -U postgres -d postgres" {
		t.Errorf("got %q, want the command unchanged", got)
	}
}

// Every container gets a shell fallback, whatever its image.
func TestSuggestAlwaysEndsWithShells(t *testing.T) {
	for _, image := range []string{"mysql:8.0", "some/unknown-image:latest", ""} {
		commands := Suggest(image, nil)
		if len(commands) < 2 {
			t.Fatalf("Suggest(%q) returned %d commands, want at least the shells", image, len(commands))
		}
		last := commands[len(commands)-2:]
		if argv(last[0]) != "bash" || argv(last[1]) != "sh" {
			t.Errorf("Suggest(%q) ends with %v, want bash then sh", image, labels(last))
		}
	}
}

func TestSuggestUnknownImageOffersOnlyShells(t *testing.T) {
	commands := Suggest("alpine:3.20", nil)

	if len(commands) != 2 {
		t.Errorf("got %v, want only the shell fallbacks", labels(commands))
	}
}

// Suggest must not hand back a slice that later calls can mutate through the
// shared shells backing array.
func TestSuggestDoesNotShareMutableState(t *testing.T) {
	first := Suggest("mysql:8.0", map[string]string{"MYSQL_ROOT_PASSWORD": "one"})
	second := Suggest("mysql:8.0", map[string]string{"MYSQL_ROOT_PASSWORD": "two"})

	if strings.Contains(argv(find(t, first, "mysql as root")), "two") {
		t.Error("the first result was mutated by the second call")
	}
	if got := argv(second[len(second)-1]); got != "sh" {
		t.Errorf("got %q as the final fallback, want sh", got)
	}
}
