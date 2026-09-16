// Package migrations embeds the SQL schema files so a deployed binary carries
// its own migrations and no separate artifact has to travel with it.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
