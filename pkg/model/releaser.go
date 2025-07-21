package model

type Releaser func(queryHeaders, responseHeaders [][2][]byte)

var emptyReleaser Releaser
