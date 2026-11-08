package scrapers

// The published descriptions of this slice's tools, taken from the Python
// server's docstrings unchanged. A client shows them to a model, so a reworded
// one is a different tool surface even where the behaviour is the same.

const WebCrawlDescription = `Fetch specific web pages and return their cleaned content as markdown.

        Use this to read a page the user names, or to spider a site from a
        starting URL. Do NOT use it to find pages on a topic — use
        soloclef_google_search for discovery. Returns one item per crawled
        page: url, title, and the page text as markdown.
        Example: start_urls=['https://blog.example.com'], max_crawl_depth=1,
        include_url_patterns=['/2026/'].
        `

const GoogleSearchDescription = `Scrape Google Search result pages for one or more queries.

        Use this to discover pages on the open web by topic; follow up with
        soloclef_web_crawl to read a result in full. Do NOT use it for
        Reddit, YouTube, or Google Maps research — the dedicated tools return
        richer data. Returns each query's parsed results: title, url, and
        snippet per organic result.
        Example: queries=['notebooklm review'], site='news.ycombinator.com'.
        `

const RedditScrapeDescription = `Search or scrape Reddit: posts, comments, subreddits, and users.

        Use this for ANY Reddit research — finding relevant subreddits or
        communities for a topic, top posts, or discussions — instead of a
        generic web search. Returns posts (title, text, score, subreddit, url)
        with comment threads unless skip_comments is set. Every post carries
        its subreddit, so to find communities for a topic, search posts and
        aggregate their subreddits.
        Example: search_queries=['NotebookLM'], sort='top', time_filter='month'.
        `

const YoutubeScrapeDescription = `Search or scrape YouTube videos, optionally with transcripts.

        Use this for YouTube research: finding videos on a topic, or reading a
        video's details or transcript. For a video's comment section use
        soloclef_youtube_comments instead. Returns per-video metadata (title,
        channel, views, description, url) and, if requested, the transcript.
        Example: search_queries=['NotebookLM tutorial'], download_subtitles=True.
        `

const YoutubeCommentsDescription = `Fetch the comments (and replies) on one or more YouTube videos.

        Use this when the user wants a video's discussion or audience reaction
        rather than the video itself; get video URLs from
        soloclef_youtube_scrape if you only have a topic. Returns comment
        text, author, likes, and replies.
        Example: urls=['https://www.youtube.com/watch?v=abc123'], max_comments=50.
        `

const InstagramScrapeDescription = `Scrape public Instagram posts or reels from URLs or search queries.

        Use this for Instagram content research: a creator's recent posts, a
        single post/reel, or public posts and reels found by keyword. For a
        profile's metadata use soloclef_instagram_details. Returns per-item
        caption, likes, comments count, media URLs, and owner. Anonymous-only:
        hashtag/place feeds and comment threads are login-walled and unavailable.
        Example: urls=['https://www.instagram.com/natgeo/'], result_type='reels'.
        `

const InstagramDetailsDescription = `Fetch Instagram profile metadata.

        Use this for entity lookups: a profile's follower/post counts and bio.
        For a feed of posts use soloclef_instagram_scrape instead. Each item
        carries a detailKind field (always "profile"). Anonymous-only: hashtag
        and place details are login-walled and unavailable.
        Example: urls=['https://www.instagram.com/natgeo/'].
        `

const TiktokScrapeDescription = `Scrape public TikTok videos by hashtag, profile, or URL.

        Use for TikTok video research — a creator's videos, a hashtag feed, or a
        specific video/profile/hashtag URL — instead of a generic web search.
        Returns videos with text, author, stats, music, and the web URL. There is
        no keyword-video search; for accounts by keyword use
        soloclef_tiktok_user_search. Example: hashtags=['food'], max_items=20.
        `

const TiktokCommentsDescription = `Scrape the public comments of TikTok videos.

        Returns each comment's text, author, like count, and reply count (replies
        carry the parent comment id). Example: video_urls=['https://www.tiktok.com/
        @nasa/video/123'], max_items=50.
        `

const TiktokUserSearchDescription = `Find public TikTok accounts by keyword.

        Returns matching profiles with name, followers, bio, and verification —
        the reliable account-discovery path (video search is login-walled).
        Example: queries=['space agency'], max_items=20.
        `

const TiktokTrendingDescription = `Get the current trending TikTok videos from the Explore feed.

        No input needed beyond how many to return; each video comes with caption,
        author, stats, music, and its web URL. Example: max_items=30.
        `

const GoogleMapsScrapeDescription = `Find places on Google Maps by search, URL, or place id.

        Use this for local-business and location research: names, addresses,
        ratings, categories, coordinates, place ids. For a place's customer
        reviews use soloclef_google_maps_reviews instead.
        Example: search_queries=['ramen'], location='Osaka, Japan', max_places=5.
        `

const GoogleMapsReviewsDescription = `Fetch customer reviews for Google Maps places by URL or place id.

        Use this to read feedback on specific places; get urls or place_ids
        from soloclef_google_maps_scrape first if you only have a name.
        Returns review text, rating, author, and date per review.
        Example: place_ids=['ChIJj61dQgK6j4AR...'], sort_by='newest'.
        `

const IndeedScrapeDescription = `Search or scrape public Indeed job postings.

        Use this for ANY Indeed job research — openings for a role, who is hiring
        at a company, salaries for a title in a location, or remote roles —
        instead of a generic web search. Returns jobs with title, company,
        location, salary, job types, and description; set scrape_job_details for
        the full description per job.
        Example: search_queries=['data analyst'], location='Remote', max_items=30.
        `

const AmazonScrapeDescription = `Scrape public Amazon product data by URL or search term.

        Use this for product research: title, price, list price, rating and
        review breakdown, availability, images, features, best-seller ranks,
        marketplace offers, sellers, and on-page reviews. Only public,
        anonymous data — no login. Provide search_terms to discover products
        or urls to target specific products, searches, categories, or
        best-seller pages.
        Example: search_terms=['mechanical keyboard'], domain='www.amazon.com',
        max_items=5, max_offers=3.
        `

const WalmartScrapeDescription = `Scrape public Walmart product data by URL or search term.

        Use this for product research: title, price, list price, rating and
        review count, availability, seller (Walmart 1P vs marketplace), images,
        description, variants, and a sample of on-page reviews. Only public,
        anonymous data — no login. For a product's full review history use
        soloclef_walmart_reviews instead.
        Example: search_terms=['air fryer'], max_items=5.
        `

const WalmartReviewsDescription = `Fetch deep paginated customer reviews for Walmart products.

        Use this to read the full review history on specific products; get urls
        or item_ids from soloclef_walmart_scrape first if you only have a name.
        Returns rating, title, text, author, verified-purchase flag, images, and
        seller response per review.
        Example: item_ids=['212092810'], sort_by='most-helpful', max_reviews=100.
        `

const ListScraperRunsDescription = `List recent scraper runs in the workspace, newest first.

        Use this to find the run_id of an earlier scrape — for example when an
        inline result was truncated — then fetch it in full with
        soloclef_get_scraper_run. Returns each run's id, capability, status,
        item count, and creation time.
        Example: capability='reddit.scrape', status='success'.
        `

const GetScraperRunDescription = `Fetch a single scraper run in full, including its stored output.

        Use this to retrieve the complete, untruncated result of an earlier
        scrape. Do NOT re-run a scraper just to recover a truncated result —
        fetch the stored run instead.
        `
