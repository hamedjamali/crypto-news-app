package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bregydoc/gtranslate"
	"github.com/go-shiori/go-readability"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"

	// "github.com/gofiber/fiber/v3/middleware/cors"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mmcdole/gofeed"
)

// ==================== مدل‌ها ====================
type NewsItem struct {
	ID                int       `json:"id"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Link              string    `json:"link"`
	Source            string    `json:"source"`
	Category          string    `json:"category"`
	Sentiment         string    `json:"sentiment"`
	PublishedAt       time.Time `json:"published_at"`
	FullContent       string    `json:"full_content"`
	TranslatedContent string    `json:"translated_content"`
	TranslatedTitle   string    `json:"translated_title"`
}

type RSSSource struct {
	Name string
	URL  string
}

// ==================== دسته‌بندی ====================
var categories = map[string][]string{
	"بیت‌کوین":    {"bitcoin", "btc", "halving", "satoshi", "بیت‌کوین"},
	"اتریوم":      {"ethereum", "eth", "vitalik", "erc-20", "اتریوم"},
	"آلت‌کوین‌ها": {"solana", "cardano", "polkadot", "avalanche", "chainlink", "altcoin"},
	"مقررات":      {"sec", "regulation", "lawsuit", "ban", "legal", "قانون"},
	"دیفای":       {"defi", "decentralized finance", "uniswap", "aave", "liquidity"},
	"ETF و نهادی": {"etf", "blackrock", "fidelity", "institutional", "custody"},
	"تکنولوژی":    {"blockchain", "layer 2", "scaling", "upgrade", "protocol"},
}

// ==================== تحلیل احساسات ====================
var positiveWords = []string{
	"surge", "soar", "rally", "bullish", "gain", "rise", "jump", "high",
	"record", "boost", "positive", "growth", "adopt", "approve", "support",
	"breakthrough", "partnership", "institutional", "win", "up", "recover",
	"صعود", "رشد", "افزایش", "مثبت",
}

var negativeWords = []string{
	"crash", "plunge", "drop", "fall", "bearish", "loss", "decline", "low",
	"ban", "hack", "scam", "fraud", "lawsuit", "sec charges", "fear",
	"sell-off", "dump", "panic", "warning", "risk", "down", "collapse",
	"ریزش", "سقوط", "کاهش", "منفی",
}

func analyzeSentiment(title, description string) string {
	text := strings.ToLower(title + " " + description)
	posScore := 0
	negScore := 0

	for _, w := range positiveWords {
		if strings.Contains(text, w) {
			posScore++
		}
	}
	for _, w := range negativeWords {
		if strings.Contains(text, w) {
			negScore++
		}
	}

	if posScore > negScore {
		return "positive"
	} else if negScore > posScore {
		return "negative"
	}
	return "neutral"
}

// ==================== منابع RSS ====================
var rssFeeds = []RSSSource{
	{Name: "CoinDesk", URL: "https://www.coindesk.com/arc/outboundfeeds/rss/"},
	{Name: "Cointelegraph", URL: "https://cointelegraph.com/rss"},
	{Name: "The Block", URL: "https://www.theblock.co/rss.xml"},
	{Name: "Decrypt", URL: "https://decrypt.co/feed"},
	{Name: "Bitcoin Magazine", URL: "https://bitcoinmagazine.com/.rss/full/"},
	{Name: "CryptoPotato", URL: "https://cryptopotato.com/feed/"},
	{Name: "NewsBTC", URL: "https://www.newsbtc.com/feed/"},
	{Name: "Bitcoinist", URL: "https://bitcoinist.com/feed/"},
}

var db *sql.DB
var translateSemaphore = make(chan struct{}, 1) // فقط یک ترجمه همزمان

// ==================== WebSocket Hub ====================
type Hub struct {
	clients    map[*websocket.Conn]bool
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	broadcast  chan []byte
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]bool),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		broadcast:  make(chan []byte, 256),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()
			log.Printf("🟢 کلاینت متصل شد. تعداد کل: %d", len(h.clients))

		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				conn.Close()
			}
			h.mu.Unlock()
			log.Printf("🔴 کلاینت قطع شد. تعداد کل: %d", len(h.clients))

		case message := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.clients {
				go func(c *websocket.Conn) {
					if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
						h.mu.Lock()
						delete(h.clients, c)
						c.Close()
						h.mu.Unlock()
					}
				}(conn)
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) Broadcast(message []byte) {
	select {
	case h.broadcast <- message:
	default:
		log.Println("⚠️ کانال broadcast پر است")
	}
}

var hub = NewHub()

// ==================== دیتابیس ====================
func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "/app/data/news.db")
	if err != nil {
		log.Fatal(err)
	}

	createTable := `
	CREATE TABLE IF NOT EXISTS news (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT,
		link TEXT UNIQUE NOT NULL,
		source TEXT,
		category TEXT,
		sentiment TEXT DEFAULT 'neutral',
		published_at DATETIME,
		full_content TEXT,
		translated_content TEXT,
		translated_title TEXT
	);`
	if _, err := db.Exec(createTable); err != nil {
		log.Fatal(err)
	}
}

// ==================== دسته‌بندی ====================
func categorize(title, description string) string {
	text := strings.ToLower(title + " " + description)
	for category, keywords := range categories {
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				return category
			}
		}
	}
	return "عمومی"
}

// ==================== استخراج متن کامل ====================
func extractFullContent(url string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CryptoNewsBot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	article, err := readability.FromReader(resp.Body, req.URL)
	if err != nil {
		return "", err
	}

	content := article.Content
	// حذف تگ‌های HTML
	content = stripHTML(content)
	content = strings.Join(strings.Fields(content), " ")

	if len(content) > 8000 {
		content = content[:8000] + "..."
	}
	return content, nil
}

func stripHTML(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			result.WriteRune(' ')
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// ==================== ترجمه ====================
func translateToPersian(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	if len(text) > 4500 {
		text = text[:4500]
	}

	// گرفتن توکن (اگر کسی در حال ترجمه باشد، اینجا منتظر می‌ماند)
	translateSemaphore <- struct{}{}
	defer func() { <-translateSemaphore }() // آزاد کردن توکن

	translated, err := gtranslate.TranslateWithParams(
		text,
		gtranslate.TranslationParams{
			From: "en",
			To:   "fa",
		},
	)
	if err != nil {
		return "", err
	}
	return translated, nil
}

// ==================== پردازش یک خبر ====================
func processArticle(item *gofeed.Item, sourceName string) {
	var published time.Time
	if item.PublishedParsed != nil {
		published = *item.PublishedParsed
	} else {
		published = time.Now()
	}

	desc := stripHTML(item.Description)
	if len(desc) > 500 {
		desc = desc[:500] + "..."
	}

	category := categorize(item.Title, desc)
	sentiment := analyzeSentiment(item.Title, desc)

	// ============ ۱. اول ذخیره فوری با متن RSS ============
	result, err := db.Exec(
		`INSERT OR IGNORE INTO news 
		 (title, description, link, source, category, sentiment, published_at, 
		  full_content, translated_content, translated_title)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Title, desc, item.Link, sourceName, category, sentiment,
		published, desc, "", item.Title, // ترجمه خالی
	)
	if err != nil {
		log.Printf("❌ خطا در ذخیره %s: %v", item.Link, err)
		return
	}

	id, _ := result.LastInsertId()
	if id == 0 {
		return // تکراری
	}

	newsItem := NewsItem{
		ID: int(id), Title: item.Title, Description: desc, Link: item.Link,
		Source: sourceName, Category: category, Sentiment: sentiment,
		PublishedAt: published, FullContent: desc,
		TranslatedContent: "", TranslatedTitle: item.Title,
	}

	msg, _ := json.Marshal(map[string]interface{}{
		"type": "new_news", "data": newsItem,
	})
	hub.Broadcast(msg)
	log.Printf("💾 ذخیره شد: %s", item.Title)

	// ============ ۲. حالا در پس‌زمینه ترجمه کن ============
	go enrichArticle(int(id), item.Link, item.Title, desc)
}

// enrichArticle: استخراج متن کامل + ترجمه، بعد UPDATE
func enrichArticle(id int, link, title, desc string) {
	// استخراج متن کامل
	fullContent, err := extractFullContent(link)
	if err != nil {
		log.Printf("⚠️ استخراج %s: %v", link, err)
		fullContent = desc
	}

	// ترجمه با timeout
	translatedTitle := safeTranslate(title)
	translatedContent := safeTranslate(fullContent)

	// UPDATE دیتابیس
	_, err = db.Exec(
		`UPDATE news SET full_content = ?, translated_content = ?, translated_title = ? WHERE id = ?`,
		fullContent, translatedContent, translatedTitle, id,
	)
	if err != nil {
		log.Printf("⚠️ UPDATE %d: %v", id, err)
		return
	}

	// ارسال آپدیت به کلاینت‌ها
	updateMsg, _ := json.Marshal(map[string]interface{}{
		"type": "update_news",
		"data": map[string]interface{}{
			"id":                 id,
			"full_content":       fullContent,
			"translated_content": translatedContent,
			"translated_title":   translatedTitle,
		},
	})
	hub.Broadcast(updateMsg)
	log.Printf("✅ ترجمه شد: %s", title)
}

// safeTranslate: ترجمه با timeout (جلوگیری از hang)
func safeTranslate(text string) string {
	if text == "" {
		return ""
	}
	if len(text) > 4500 {
		text = text[:4500]
	}

	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		t, err := gtranslate.TranslateWithParams(
			text,
			gtranslate.TranslationParams{From: "en", To: "fa"},
		)
		ch <- result{t, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return ""
		}
		return r.text
	case <-time.After(15 * time.Second):
		log.Printf("⏱️ timeout در ترجمه")
		return ""
	}
}
// func processArticle(item *gofeed.Item, sourceName string) {
// 	var published time.Time
// 	if item.PublishedParsed != nil {
// 		published = *item.PublishedParsed
// 	} else {
// 		published = time.Now()
// 	}

// 	desc := stripHTML(item.Description)
// 	if len(desc) > 500 {
// 		desc = desc[:500] + "..."
// 	}

// 	category := categorize(item.Title, desc)
// 	sentiment := analyzeSentiment(item.Title, desc)

// 	// استخراج متن کامل
// 	fullContent, err := extractFullContent(item.Link)
// 	if err != nil {
// 		log.Printf("⚠️ خطا در استخراج %s: %v", item.Link, err)
// 		fullContent = desc
// 	}

// 	// ترجمه متن کامل
// 	translatedContent, err := translateToPersian(fullContent)
// 	if err != nil {
// 		log.Printf("⚠️ خطا در ترجمه متن %s: %v", item.Link, err)
// 		translatedContent = "ترجمه در دسترس نیست"
// 	}

// 	// ترجمه عنوان
// 	translatedTitle, err := translateToPersian(item.Title)
// 	if err != nil {
// 		translatedTitle = item.Title
// 	}

// 	// ذخیره در دیتابیس
// 	result, err := db.Exec(
// 		`INSERT OR IGNORE INTO news 
// 		 (title, description, link, source, category, sentiment, published_at, full_content, translated_content, translated_title)
// 		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
// 		item.Title, desc, item.Link, sourceName, category, sentiment,
// 		published, fullContent, translatedContent, translatedTitle,
// 	)
// 	if err != nil {
// 		log.Printf("خطا در ذخیره: %v", err)
// 		return
// 	}

// 	// اگر درج انجام شد، شناسه رو بگیر
// 	id, _ := result.LastInsertId()
// 	if id == 0 {
// 		return // خبر تکراری بود
// 	}

// 	newsItem := NewsItem{
// 		ID:                int(id),
// 		Title:             item.Title,
// 		Description:       desc,
// 		Link:              item.Link,
// 		Source:            sourceName,
// 		Category:          category,
// 		Sentiment:         sentiment,
// 		PublishedAt:       published,
// 		FullContent:       fullContent,
// 		TranslatedContent: translatedContent,
// 		TranslatedTitle:   translatedTitle,
// 	}

// 	msg, _ := json.Marshal(map[string]interface{}{
// 		"type": "new_news",
// 		"data": newsItem,
// 	})
// 	hub.Broadcast(msg)
// 	log.Printf("✅ خبر جدید: %s", item.Title)
// }

// ==================== دریافت فید ====================
func fetchFeed(source RSSSource, wg *sync.WaitGroup) {
	defer wg.Done()

	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(source.URL)
	if err != nil {
		log.Printf("❌ خطا در خواندن %s: %v", source.Name, err)
		return
	}

	log.Printf("📥 %s: %d خبر یافت شد", source.Name, len(feed.Items))

	var innerWg sync.WaitGroup
	sem := make(chan struct{}, 1) // حداکثر ۳ پردازش همزمان

	for _, item := range feed.Items {
		// بررسی تکراری بودن قبل از پردازش سنگین
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM news WHERE link = ?", item.Link).Scan(&exists)
		if exists > 0 {
			continue
		}

		innerWg.Add(1)
		sem <- struct{}{}
		go func(it *gofeed.Item) {
			defer innerWg.Done()
			defer func() { <-sem }()
			processArticle(it, source.Name)
		}(item)
	}

	innerWg.Wait()
}

// ==================== به‌روزرسانی همه فیدها ====================
func fetchAllFeeds() {
	var wg sync.WaitGroup
	for _, source := range rssFeeds {
		wg.Add(1)
		go fetchFeed(source, &wg)
	}
	wg.Wait()
	log.Println("🔄 همه فیدها به‌روزرسانی شدند")
}

// ==================== Main ====================
func main() {
	initDB()
	defer db.Close()

	go hub.Run()
	go fetchAllFeeds()

	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		for range ticker.C {
			fetchAllFeeds()
		}
	}()

	app := fiber.New(fiber.Config{
		AppName: "Crypto News Aggregator",
	})

	// app.Use(cors.New(cors.Config{
	// 	AllowOrigins: "http://localhost:5173",
	// 	AllowHeaders: "Origin, Content-Type, Accept",
	// }))

	// ---------- WebSocket ----------
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		hub.register <- c
		defer func() {
			hub.unregister <- c
		}()

		// ارسال اخبار اولیه
		rows, err := db.Query(`SELECT id, title, description, link, source, category, 
			sentiment, published_at, translated_title 
			FROM news ORDER BY published_at DESC LIMIT 30`)
		if err == nil {
			var news []NewsItem
			for rows.Next() {
				var n NewsItem
				rows.Scan(&n.ID, &n.Title, &n.Description, &n.Link, &n.Source,
					&n.Category, &n.Sentiment, &n.PublishedAt, &n.TranslatedTitle)
				news = append(news, n)
			}
			rows.Close()
			if len(news) > 0 {
				initMsg, _ := json.Marshal(map[string]interface{}{
					"type": "initial_news",
					"data": news,
				})
				c.WriteMessage(websocket.TextMessage, initMsg)
			}
		}

		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				break
			}
		}
	}))

	// ---------- API: لیست اخبار ----------
	app.Get("/api/news", func(c fiber.Ctx) error {
		category := c.Query("category", "")
		limit := fiber.Query[int](c, "limit", 50)

		query := `SELECT id, title, description, link, source, category, sentiment, 
			published_at, translated_title FROM news`
		args := []interface{}{}

		if category != "" && category != "همه" {
			query += " WHERE category = ?"
			args = append(args, category)
		}

		query += " ORDER BY published_at DESC LIMIT ?"
		args = append(args, limit)

		rows, err := db.Query(query, args...)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		defer rows.Close()

		var news []NewsItem
		for rows.Next() {
			var n NewsItem
			if err := rows.Scan(&n.ID, &n.Title, &n.Description, &n.Link,
				&n.Source, &n.Category, &n.Sentiment, &n.PublishedAt,
				&n.TranslatedTitle); err != nil {
				continue
			}
			news = append(news, n)
		}
		if news == nil {
			news = []NewsItem{}
		}
		return c.JSON(news)
	})

	// ---------- API: جزئیات یک خبر ----------
	app.Get("/api/news/:id", func(c fiber.Ctx) error {
		id := c.Params("id")
		var n NewsItem
		err := db.QueryRow(
			`SELECT id, title, description, link, source, category, sentiment,
				published_at, full_content, translated_content, translated_title
			 FROM news WHERE id = ?`, id,
		).Scan(&n.ID, &n.Title, &n.Description, &n.Link, &n.Source, &n.Category,
			&n.Sentiment, &n.PublishedAt, &n.FullContent, &n.TranslatedContent,
			&n.TranslatedTitle)
		if err != nil {
			return c.Status(404).JSON(fiber.Map{"error": "خبر پیدا نشد"})
		}
		return c.JSON(n)
	})

	// ---------- API: دسته‌بندی‌ها ----------
	app.Get("/api/categories", func(c fiber.Ctx) error {
		rows, err := db.Query("SELECT DISTINCT category FROM news ORDER BY category")
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		defer rows.Close()

		cats := []string{"همه"}
		for rows.Next() {
			var cat string
			rows.Scan(&cat)
			cats = append(cats, cat)
		}
		return c.JSON(cats)
	})

	// ---------- API: منابع ----------
	app.Get("/api/sources", func(c fiber.Ctx) error {
		return c.JSON(rssFeeds)
	})

	// ---------- API: به‌روزرسانی دستی ----------
	app.Post("/api/refresh", func(c fiber.Ctx) error {
		go fetchAllFeeds()
		return c.JSON(fiber.Map{"message": "در حال به‌روزرسانی..."})
	})

	log.Println("🚀 سرور روی http://localhost:3000 اجرا شد")
	log.Fatal(app.Listen(":3000"))
}
