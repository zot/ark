module ark

go 1.25.3

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/anthropics/microvec v0.0.0
	github.com/bmatsuo/lmdb-go v1.8.0
	microfts2 v0.0.0
)

require github.com/google/codesearch v1.2.0 // indirect

replace (
	github.com/anthropics/microvec => ../microvec
	microfts2 => ../microfts2
)
