"""pymupdf4llm behind one HTTP endpoint.

The one Python process the Go indexing pipeline keeps, because nothing in Go
runs MuPDF's layout model. It holds the parser and a web server from the
standard library, and imports nothing from the application.
"""
