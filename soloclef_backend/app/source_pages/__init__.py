"""Source pages: which page of an original PDF each chunk was read from.

A citation names a chunk; knowing the chunk's page lets the web app open the
original PDF at that page. Pages come from the parser when it reports page
breaks, and otherwise from the PDF's own text layer.
"""
