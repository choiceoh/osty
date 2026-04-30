package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestQtQuickDoctorReportHealthyWithoutCMakeProbe(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "osty.toml"), `[package]
name = "desk"
version = "0.1.0"
edition = "0.5"

[gui]
backend = "qtquick"
entry = "ui/main.qml"

[target.`+runtime.GOARCH+`-`+runtime.GOOS+`]
link = ["osty_qt"]
`)
	mustWrite(t, filepath.Join(root, "main.osty"), "fn main() { }\n")
	mustWrite(t, filepath.Join(root, "ui", "main.qml"), "import QtQuick\nimport QtQuick.Controls\n")
	bridge := makeFakeQtBridgeRoot(t)

	report := qtQuickDoctorReport(qtQuickDoctorOptions{
		Start:      root,
		BridgeRoot: bridge,
		RunCMake:   false,
	})
	if reportHasFailure(report) {
		t.Fatalf("report has failure: %#v", report.Checks)
	}
	if !doctorHasCheck(report, "qml entry", guiDoctorPass) {
		t.Fatalf("report missing passing qml entry check: %#v", report.Checks)
	}
	if !doctorHasCheck(report, "qt configure", guiDoctorWarn) {
		t.Fatalf("report missing skipped cmake warning: %#v", report.Checks)
	}
}

func TestQtQuickDoctorReportCatchesMissingQMLEntry(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "osty.toml"), `[package]
name = "desk"
version = "0.1.0"
edition = "0.5"

[gui]
backend = "qtquick"
entry = "ui/missing.qml"
`)
	mustWrite(t, filepath.Join(root, "main.osty"), "fn main() { }\n")

	report := qtQuickDoctorReport(qtQuickDoctorOptions{
		Start:      root,
		BridgeRoot: makeFakeQtBridgeRoot(t),
		RunCMake:   false,
	})
	if !doctorHasCheck(report, "qml entry", guiDoctorFail) {
		t.Fatalf("report missing failing qml entry check: %#v", report.Checks)
	}
}

func makeFakeQtBridgeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CMakeLists.txt"), "cmake_minimum_required(VERSION 3.20)\n")
	mustWrite(t, filepath.Join(root, "include", "osty_qt.h"), "")
	mustWrite(t, filepath.Join(root, "src", "osty_qt.cpp"), "")
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func doctorHasCheck(report guiDoctorReport, name string, status guiDoctorStatus) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
