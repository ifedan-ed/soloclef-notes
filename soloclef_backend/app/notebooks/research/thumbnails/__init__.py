"""Result thumbnails served through our own origin.

Scraped image URLs point at platform CDNs over whatever scheme they use, and a
browser fetching them directly would leak the viewer's IP and referrer to each
platform and mix http content into the page. So rows carry a signed link to a
small image proxy instead: only URLs this server put in a result can be
fetched through it, and nothing of the viewer's (cookies, headers) is passed on.
"""
