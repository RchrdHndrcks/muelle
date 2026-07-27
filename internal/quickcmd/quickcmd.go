// Package quickcmd suggests useful commands to run inside a container, based
// on its image and environment.
//
// The point is to remove the two things that make "get me a MySQL prompt in
// that container" tedious: remembering the client's flags, and finding the
// password that was set when the container was created. The daemon already
// knows both, so the menu can be pre-filled.
package quickcmd

import (
	"strings"
)

// Command is one runnable suggestion.
type Command struct {
	// Label is what the user sees in the menu.
	Label string
	// Argv is the command and its arguments, passed to "docker exec"
	// without shell interpretation, so values containing spaces or quotes
	// need no escaping.
	Argv []string
	// Sensitive marks commands whose arguments contain a credential, so
	// the UI can avoid echoing them.
	Sensitive bool
}

// String renders the command for display, masking credentials.
//
// The menu is on screen, and screens get shared and screenshotted, so a
// password must never be rendered. Two shapes need covering: attached
// ("-psecret", as MySQL requires) and detached ("-p", "secret", as mongosh and
// redis-cli take it).
func (c Command) String() string {
	if !c.Sensitive {
		return strings.Join(c.Argv, " ")
	}
	masked := make([]string, len(c.Argv))
	copy(masked, c.Argv)
	for i := 0; i < len(masked); i++ {
		switch {
		case masked[i] == "-p" || masked[i] == "-a" || masked[i] == "--password":
			if i+1 < len(masked) {
				masked[i+1] = mask(masked[i+1])
				i++
			}
		case strings.HasPrefix(masked[i], "-p") && len(masked[i]) > 2:
			masked[i] = "-p" + mask(masked[i][2:])
		}
	}
	return strings.Join(masked, " ")
}

// mask replaces a secret with a fixed-width run of asterisks. Fixed width, not
// proportional: the length of a password is itself information.
func mask(string) string { return "********" }

// shells are always offered last, so there is a fallback when the tailored
// suggestions do not fit. bash first: if the image has it, it is the nicer
// prompt; sh is guaranteed on practically every image.
var shells = []Command{
	{Label: "bash shell", Argv: []string{"bash"}},
	{Label: "sh shell", Argv: []string{"sh"}},
}

// Suggest returns candidate commands for a container, most specific first,
// always ending with the shells.
//
// image is matched as a substring so registry prefixes and tags do not
// interfere: "docker.io/library/mysql:8.0" matches "mysql". env is the
// container's environment, used to pre-fill credentials.
func Suggest(image string, env map[string]string) []Command {
	var commands []Command
	name := strings.ToLower(image)

	switch {
	case containsAny(name, "mysql", "mariadb", "percona"):
		commands = append(commands, mysqlCommands(env)...)
	case containsAny(name, "postgres", "pgvector", "timescale", "postgis"):
		commands = append(commands, postgresCommands(env)...)
	case containsAny(name, "redis", "valkey"):
		commands = append(commands, redisCommands(env)...)
	case containsAny(name, "mongo"):
		commands = append(commands, mongoCommands(env)...)
	case containsAny(name, "rabbitmq"):
		commands = append(commands,
			Command{Label: "rabbitmqctl status", Argv: []string{"rabbitmqctl", "status"}},
			Command{Label: "list queues", Argv: []string{"rabbitmqctl", "list_queues"}},
		)
	case containsAny(name, "nginx"):
		commands = append(commands,
			Command{Label: "test config", Argv: []string{"nginx", "-t"}},
			Command{Label: "reload config", Argv: []string{"nginx", "-s", "reload"}},
		)
	case containsAny(name, "node"):
		commands = append(commands, Command{Label: "node repl", Argv: []string{"node"}})
	case containsAny(name, "python"):
		commands = append(commands, Command{Label: "python repl", Argv: []string{"python3"}})
	}

	return append(commands, shells...)
}

// mysqlCommands builds a client invocation with credentials from the
// environment. The official image sets MYSQL_ROOT_PASSWORD (or one of the
// alternatives below) at creation time, which is exactly the value nobody
// remembers when they need it.
//
// mariadb images ship the client as "mariadb" in recent versions but keep
// "mysql" as a symlink, so "mysql" is the safer invocation for both.
func mysqlCommands(env map[string]string) []Command {
	user := firstNonEmpty(env, "MYSQL_USER", "MARIADB_USER")
	password := firstNonEmpty(env, "MYSQL_PASSWORD", "MARIADB_PASSWORD")
	// Deliberately not consulting the *_FILE variants: those hold a path to
	// a secret, not the secret, and passing one to -p yields a command that
	// fails in a confusing way.
	rootPassword := firstNonEmpty(env, "MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD")
	database := firstNonEmpty(env, "MYSQL_DATABASE", "MARIADB_DATABASE")

	var commands []Command

	rootArgv := []string{"mysql", "-u", "root"}
	if rootPassword != "" {
		// MySQL's client requires the password attached to -p with no
		// space; "-p secret" is parsed as a database name.
		rootArgv = append(rootArgv, "-p"+rootPassword)
	}
	if database != "" {
		rootArgv = append(rootArgv, database)
	}
	commands = append(commands, Command{
		Label:     labelWithDatabase("mysql as root", database),
		Argv:      rootArgv,
		Sensitive: rootPassword != "",
	})

	if user != "" && user != "root" {
		userArgv := []string{"mysql", "-u", user}
		if password != "" {
			userArgv = append(userArgv, "-p"+password)
		}
		if database != "" {
			userArgv = append(userArgv, database)
		}
		commands = append(commands, Command{
			Label:     labelWithDatabase("mysql as "+user, database),
			Argv:      userArgv,
			Sensitive: password != "",
		})
	}

	dumpArgv := []string{"mysqldump", "-u", "root"}
	if rootPassword != "" {
		dumpArgv = append(dumpArgv, "-p"+rootPassword)
	}
	if database != "" {
		dumpArgv = append(dumpArgv, database)
	} else {
		dumpArgv = append(dumpArgv, "--all-databases")
	}
	commands = append(commands, Command{
		Label:     "mysqldump",
		Argv:      dumpArgv,
		Sensitive: rootPassword != "",
	})

	return commands
}

// postgresCommands builds psql invocations. The password does not go on the
// command line: psql reads PGPASSWORD from the environment, and inside the
// container the postgres superuser can connect over the local socket without
// one at all.
func postgresCommands(env map[string]string) []Command {
	user := firstNonEmpty(env, "POSTGRES_USER")
	if user == "" {
		user = "postgres"
	}
	database := firstNonEmpty(env, "POSTGRES_DB")
	if database == "" {
		database = user
	}

	return []Command{
		{
			Label: labelWithDatabase("psql as "+user, database),
			Argv:  []string{"psql", "-U", user, "-d", database},
		},
		{
			Label: "list databases",
			Argv:  []string{"psql", "-U", user, "-d", database, "-c", `\l`},
		},
		{
			Label: "pg_dump",
			Argv:  []string{"pg_dump", "-U", user, database},
		},
	}
}

// redisCommands builds redis-cli invocations, authenticating only when the
// image was given a password.
func redisCommands(env map[string]string) []Command {
	password := firstNonEmpty(env, "REDIS_PASSWORD", "VALKEY_PASSWORD")

	base := []string{"redis-cli"}
	if password != "" {
		base = append(base, "-a", password)
	}

	monitor := append(append([]string{}, base...), "monitor")
	info := append(append([]string{}, base...), "info")

	return []Command{
		{Label: "redis-cli", Argv: base, Sensitive: password != ""},
		{Label: "server info", Argv: info, Sensitive: password != ""},
		{Label: "monitor commands", Argv: monitor, Sensitive: password != ""},
	}
}

// mongoCommands prefers mongosh, the client shipped with MongoDB 5 and later,
// and falls back to the legacy mongo shell for older images.
func mongoCommands(env map[string]string) []Command {
	user := firstNonEmpty(env, "MONGO_INITDB_ROOT_USERNAME")
	password := firstNonEmpty(env, "MONGO_INITDB_ROOT_PASSWORD")

	argv := []string{"mongosh"}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	if password != "" {
		argv = append(argv, "-p", password)
	}

	return []Command{
		{Label: "mongosh", Argv: argv, Sensitive: password != ""},
		{Label: "mongo (legacy shell)", Argv: []string{"mongo"}},
	}
}

// labelWithDatabase appends the target database to a menu label when one is
// known, so the user can tell two otherwise identical entries apart.
func labelWithDatabase(label, database string) string {
	if database == "" {
		return label
	}
	return label + " (" + database + ")"
}

// firstNonEmpty returns the value of the first key that is set and non-empty.
func firstNonEmpty(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := env[key]; value != "" {
			return value
		}
	}
	return ""
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, substring := range substrings {
		if strings.Contains(s, substring) {
			return true
		}
	}
	return false
}
