//go:build e2e

package edgecase

import (
	"testing"

	"github.com/visdomtech/orcacommon/tests/e2e/harness"
)

var stack *harness.Stack

func TestMain(m *testing.M) {
	harness.RunMain(m, &stack)
}
