package agent

import (
	"testing"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

func TestOutputTypeFor(t *testing.T) {
	if outputTypeFor(true) != agentpb.OutputType_OUTPUT_TYPE_STDOUT {
		t.Error("stdout mismatch")
	}
	if outputTypeFor(false) != agentpb.OutputType_OUTPUT_TYPE_STDERR {
		t.Error("stderr mismatch")
	}
}
