package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDefaultOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--generate", "10000"}, &stdout, &stderr); code != 0 {
		t.Fatalf("退出码 %d：%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "count=10000 bytes=") || !strings.Contains(out, "bytes_per_item=") {
		t.Fatalf("输出格式不对：%q", out)
	}
	if strings.Contains(out, "roundtrip") || strings.Contains(out, "at[") {
		t.Fatalf("不带新参数时不该有多余行：%q", out)
	}
}

func TestRoundtripFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--values", "5,4,3,2,1", "--roundtrip"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "roundtrip=true") {
		t.Fatalf("code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}

func TestAtFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--values", "5,4,3,2,1", "--at", "2"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "at[2]=3") {
		t.Fatalf("code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}

func TestAtOutOfRange(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--values", "1,2", "--at", "5"}, &stdout, &stderr); code == 0 {
		t.Fatal("越界应返回非零退出码")
	}
}
