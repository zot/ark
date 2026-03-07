module ark

go 1.25.3

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/anthropics/microvec v0.0.0
	github.com/bmatsuo/lmdb-go v1.8.0
	github.com/zot/ui-engine v0.0.0-00010101000000-000000000000
	microfts2 v0.0.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/google/codesearch v1.2.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	github.com/zot/change-tracker v1.3.1 // indirect
	golang.org/x/sys v0.40.0 // indirect
)

replace (
	github.com/anthropics/microvec => ../microvec
	github.com/zot/ui-engine => ../ui-engine
	microfts2 => ../microfts2
)
