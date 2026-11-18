module github.com/omarcosr/youtube-music/go

go 1.25.0

require github.com/omarcosr/tarve/go v0.0.0

require github.com/ebitengine/purego v0.11.2 // indirect

// The Go host has no tagged release yet: it builds against a Tarve checkout next to this one
// (A:\tarve and A:\youtube-music, say).
replace github.com/omarcosr/tarve/go => ../../tarve/go
