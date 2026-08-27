// jsonata-inventory rebuilds the inventory spike's SQLite projection from a
// verified Gitseq repository and executes one bounded read-only query.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/spike/jsonataddl"
)

func main() {
	var repo, database, query string
	flag.StringVar(&repo, "repo", "", "Git repository bound to the inventory spike")
	flag.StringVar(&database, "database", "", "new path for the disposable SQLite projection")
	flag.StringVar(&query, "sql", "SELECT sku, available FROM stock ORDER BY sku", "read-only SQL query")
	flag.Parse()
	if repo == "" || database == "" {
		fmt.Fprintln(os.Stderr, "usage: jsonata-inventory -repo PATH -database NEW_PATH [-sql SELECT]")
		os.Exit(2)
	}
	ctx := context.Background()
	profile, err := jsonataddl.LoadInventory()
	if err != nil {
		fail(err)
	}
	workspace, err := host.Open(ctx, repo, profile.Application)
	if err != nil {
		fail(err)
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		fail(err)
	}
	projection, err := jsonataddl.Build(ctx, profile, log, database)
	if err != nil {
		fail(err)
	}
	defer projection.Close()
	result, err := projection.Query(ctx, query)
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
