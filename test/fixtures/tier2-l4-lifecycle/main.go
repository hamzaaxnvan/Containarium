// tier2-l4-lifecycle: drives a real caddy-l4 server through the operations
// the daemon performs at runtime (EnableL4ProxyProtocol → ActivateL4 →
// AddL4Route * N → RemoveL4Route → ListL4Routes) and asserts the pattern B
// wrapping is intact at every step. This is the regression test for the
// prod-broke-everything bug from attempt 3, where RouteSyncJob's CRUD
// undid the wrapping within seconds of daemon startup.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/footprintai/containarium/internal/app"
)

func main() {
	adminURL := flag.String("admin", "http://127.0.0.1:2019", "Caddy admin URL")
	trusted := flag.String("trusted", "127.0.0.0/8", "comma-separated trusted CIDRs")
	flag.Parse()

	cidrs := strings.Split(*trusted, ",")
	m := app.NewL4ProxyManager(*adminURL)

	log.Println("step 1: EnableL4ProxyProtocol (records trusted CIDRs)")
	if err := m.EnableL4ProxyProtocol(cidrs); err != nil {
		log.Fatalf("EnableL4ProxyProtocol: %v", err)
	}

	log.Println("step 2: ActivateL4 (must produce wrapped shape)")
	if err := m.ActivateL4(); err != nil {
		log.Fatalf("ActivateL4: %v", err)
	}
	if err := assertWrappedAndCatchallV2(*adminURL, "after-activate"); err != nil {
		log.Fatalf("FAIL: %v", err)
	}

	log.Println("step 3: simulate 3 RouteSyncJob cycles of AddL4Route")
	for cycle := 0; cycle < 3; cycle++ {
		if err := m.AddL4Route("passthrough-a.example", "203.0.113.1", 50051); err != nil {
			log.Fatalf("cycle %d AddL4Route grpc: %v", cycle, err)
		}
		if err := m.AddL4Route("passthrough-b.example", "203.0.113.2", 50052); err != nil {
			log.Fatalf("cycle %d AddL4Route grpc-dev: %v", cycle, err)
		}
		if err := assertWrappedAndCatchallV2(*adminURL, fmt.Sprintf("after-add-cycle-%d", cycle)); err != nil {
			log.Fatalf("FAIL: %v", err)
		}
	}

	log.Println("step 4: RemoveL4Route (must keep wrapping)")
	if err := m.RemoveL4Route("passthrough-b.example"); err != nil {
		log.Fatalf("RemoveL4Route: %v", err)
	}
	if err := assertWrappedAndCatchallV2(*adminURL, "after-remove"); err != nil {
		log.Fatalf("FAIL: %v", err)
