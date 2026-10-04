module c1book-reader

go 1.26.0

require (
	c1device v0.0.0
	golang.org/x/image v0.45.0
	golang.org/x/net v0.59.0
	golang.org/x/text v0.42.0
)

require golang.org/x/sys v0.48.0 // indirect

replace c1device => ../c1device
