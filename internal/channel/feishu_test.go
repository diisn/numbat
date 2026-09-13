package channel

import (
	"context"
	"testing"
	"time"
)

func TestFeishuConnectRequiresConfig(t *testing.T) {
	ch := NewFeishuChannel("feishu", "", "")
	if err := ch.Connect(context.Background()); err == nil {
		t.Fatal("connect without app_id/app_secret should fail")
	}
	if ch.Status() != ChannelStatusError {
		t.Fatalf("status after failed connect: %s", ch.Status())
	}
}

func TestFeishuConnectDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := NewFeishuChannel("feishu", "app-id", "app-secret")
	if err := ch.Connect(ctx); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if ch.Status() != ChannelStatusConnected {
		t.Fatalf("status after connect: %s", ch.Status())
	}

	if err := ch.Disconnect(); err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if ch.Status() != ChannelStatusDisconnected {
		t.Fatalf("status after disconnect: %s", ch.Status())
	}
	select {
	case _, ok := <-ch.Receive():
		if ok {
			t.Fatal("receive channel should be closed after disconnect")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for receive channel close")
	}
}
