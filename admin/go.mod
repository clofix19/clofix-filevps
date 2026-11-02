module github.com/clofix/filevps/admin

go 1.23.0

require (
	github.com/clofix/filevps/worker v0.0.0
	golang.org/x/crypto v0.31.0
)

replace github.com/clofix/filevps/worker => ../worker
