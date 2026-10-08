package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestReceiverRouting(t *testing.T) {
	routes := map[string]route{
		"source-a": {Container: "backupfabric-native-source-a", User: "10001:10002"},
		"source-b": {Container: "backupfabric-native-source-b", User: "20001:20002"},
	}
	command := "printf '%s\\n' 'harmless fixture'; test ! -e /other-source"
	for _, source := range []string{"source-a", "source-b"} {
		args, err := receiverArgs(routes, source, command)
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"exec", "-i", "--user", routes[source].User, "--workdir", "/data/backup", "--",
			routes[source].Container, "/bin/bash", "-c", command}
		if !reflect.DeepEqual(args, expected) {
			t.Fatalf("unexpected routing: %q", args)
		}
	}
}

func TestReceiverRejectsInvalidRequests(t *testing.T) {
	routes := map[string]route{
		"source-a": {Container: "backupfabric-native-source-a", User: "10001:10001"},
		"invalid":  {Container: "--privileged"},
	}
	for _, request := range []struct{ source, command string }{
		{"source-b", "ghe-version"},
		{"../source-a", "ghe-version"},
		{"--source-a", "ghe-version"},
		{"invalid", "ghe-version"},
		{"source-a", ""},
		{"source-a", strings.Repeat("x", 16385)},
	} {
		if _, err := receiverArgs(routes, request.source, request.command); err == nil {
			t.Fatalf("accepted invalid request for %q", request.source)
		}
	}
	if _, err := receiverArgs(routes, "source-a", strings.Repeat("x", 16384)); err != nil {
		t.Fatalf("rejected boundary command: %v", err)
	}
}

func TestReceiverRejectsUnsafeUsers(t *testing.T) {
	for _, user := range []string{"", "root", "0:10001", "10001:0", "-1:10001", "4294967295:10001", "10001:10001:0", "--privileged:1"} {
		routes := map[string]route{"tenant": {Container: "receiver", User: user}}
		if _, err := receiverArgs(routes, "tenant", "printf harmless"); err == nil {
			t.Fatalf("accepted unsafe receiver identity %q", user)
		}
	}
}
