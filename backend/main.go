package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bregydoc/gtranslate"
	"github.com/go-shiori/go-readability"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	// "github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/mmcdole/gofeed"
	_ "github.com/mattn/go-sqlite3"
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
	"بیت‌کوین":    {"bitcoin", "btc", "halving", "satoshi"},
	"اتریوم":      {"ethereum", "eth", "vitalik", "erc-20"},
	"آلت‌کوین‌ها": {"solana", "cardano", "polkadot", "avalanche", "chainlink", "altcoin"},
	"مقررات":      {"sec", "regulation", "lawsuit", "ban", "legal"},
	"دیفای":       {"defi", "decentralized finance", "uniswap", "aave", "liquidity"},
	"ETF و نهادی": {"etf", "blackrock", "fidelity", "institutional", "custody"},
	"تکنولوژی":    {"blockchain", "layer 2", "scaling", "upgrade", "protocol"},
}

// ==================== تحلیل احساسات ====================
var positiveWords = []string{
	"surge", "soar", "rally", "bullish", "gain", "rise", "jump", "high",
	"record", "boost", "growth", "adopt", "approve", "support",
	"breakthrough", "partnership", "institutional", "win", "up", "recover",
}

var negativeWords = []string{
	"crash", "plunge", "drop", "fall", "bearish", "loss", "decline", "low",
	"ban", "hack", "scam", "fraud", "lawsuit", "fear", "sell-off", "dump",
	"panic", "warning", "risk", "down", "collapse",
}

func analyzeSentiment(title, description string) string {
	text := strings.ToLower(title + " " + description)
	posScore, negScore := 0, 0

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
	dbPath := "/app/data/news.db"

	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("❌ خطا در باز کردن دیتابیس: %v", err)
	}

	if err := db.Ping(); err != nil {
		log.Fatalf("❌ نمی‌تواند به دیتابیس وصل شود: %v", err)
	}

	log.Printf("✅ دیتابیس در %s باز شد", dbPath)

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
		log.Fatalf("❌ خطا در ساخت جدول: %v", err)
	}
	log.Println("✅ جدول news آماده است")
}

// ==================== دسته‌بندی خودکار ====================
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

	content := stripHTML(article.Content)
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

// ==================== ترجمه اصلی: MyMemory + Fallback ====================
func safeTranslate(text string) string {
	if text == "" {
		return ""
	}

	// محدودیت طول برای MyMemory
	if len(text) > 4500 {
		text = text[:4500]
	}

	// تلاش ۱: MyMemory (رایگان، بدون کلید)
	if result := tryMyMemory(text); result != "" {
		return result
	}

	// تلاش ۲: Google (اگه MyMemory کار نکرد)
	if result := tryGTranslate(text); result != "" {
		return result
	}

	return ""
}

// ==================== MyMemory ====================
func tryMyMemory(text string) string {
	// اگه متن بلنده، به تکه‌های ۵۰۰ کاراکتری تقسیم کن
	if len(text) > 500 {
		var parts []string
		for i := 0; i < len(text); i += 500 {
			end := i + 500
			if end > len(text) {
				end = len(text)
			}
			parts = append(parts, text[i:end])
		}

		var translated []string
		for _, part := range parts {
			if r := tryMyMemory(part); r != "" {
				translated = append(translated, r)
			}
			time.Sleep(300 * time.Millisecond)
		}
		if len(translated) > 0 {
			return strings.Join(translated, " ")
		}
		return ""
	}

	encoded := url.QueryEscape(text)
	apiURL := fmt.Sprintf(
		"https://api.mymemory.translated.net/get?q=%s&langpair=en|fa",
		encoded,
	)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
		ResponseStatus interface{} `json:"responseStatus"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return ""
	}

	translated := result.ResponseData.TranslatedText

	// اگه خطای "PLEASE SELECT TWO DISTINCT LANGUAGES" یا مشابه داد، رد کن
	if strings.Contains(translated, "PLEASE SELECT") ||
		strings.Contains(translated, "INVALID") ||
		translated == "" {
		return ""
	}

	return translated
}

// ==================== Google (Fallback) ====================
func tryGTranslate(text string) string {
	type result struct {
		text string
	}
	ch := make(chan result, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{""}
			}
		}()

		t, err := gtranslate.TranslateWithParams(
			text,
			gtranslate.TranslationParams{From: "en", To: "fa"},
		)
		if err != nil {
			ch <- result{""}
			return
		}
		ch <- result{t}
	}()

	select {
	case r := <-ch:
		return r.text
	case <-time.After(10 * time.Second):
		return ""
	}
}

// ==================== غنی‌سازی خبر ====================
func enrichArticle(id int, link, title, desc string) {
	// ۱. استخراج متن کامل
	fullContent, err := extractFullContent(link)
	if err != nil {
		log.Printf("⚠️ استخراج %s: %v", link, err)
		fullContent = desc
	}

	// ۲. ترجمه عنوان
	translatedTitle := safeTranslate(title)
	if translatedTitle == "" {
		translatedTitle = title
	}

	// ۳. ترجمه متن کامل
	translatedContent := safeTranslate(fullContent)
	if translatedContent == "" {
		translatedContent = "ترجمه در دسترس نیست"
	}

	// ۴. UPDATE دیتابیس
	_, err = db.Exec(
		`UPDATE news SET full_content = ?, translated_content = ?, translated_title = ? WHERE id = ?`,
		fullContent, translatedContent, translatedTitle, id,
	)
	if err != nil {
		log.Printf("⚠️ UPDATE %d: %v", id, err)
		return
	}

	// ۵. ارسال آپدیت به کلاینت‌ها
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
	log.Printf("✅ ترجمه شد: %s → %s", title, translatedTitle)
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

	// ۱. ذخیره فوری
	result, err := db.Exec(
		`INSERT OR IGNORE INTO news 
		 (title, description, link, source, category, sentiment, published_at, 
		  full_content, translated_content, translated_title)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Title, desc, item.Link, sourceName, category, sentiment,
		published, desc, "", item.Title,
	)
	if err != nil {
		log.Printf("❌ خطا در ذخیره %s: %v", item.Link, err)
		return
	}

	id, _ := result.LastInsertId()
	if id == 0 {
		return
	}

	// ۲. ارسال فوری
	newsItem := NewsItem{
		ID:                int(id),
		Title:             item.Title,
		Description:       desc,
		Link:              item.Link,
		Source:            sourceName,
		Category:          category,
		Sentiment:         sentiment,
		PublishedAt:       published,
		FullContent:       desc,
		TranslatedContent: "",
		TranslatedTitle:   item.Title,
	}

	msg, _ := json.Marshal(map[string]interface{}{
		"type": "new_news",
		"data": newsItem,
	})
	hub.Broadcast(msg)
	log.Printf("💾 ذخیره شد: %s", item.Title)

	// ۳. غنی‌سازی در پس‌زمینه
	go enrichArticle(int(id), item.Link, item.Title, desc)
}

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
	sem := make(chan struct{}, 5) // کاهش به 5 برای جلوگیری از rate limit

	for _, item := range feed.Items {
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
			defer func() {
				if r := recover(); r != nil {
					log.Printf("⚠️ panic: %v", r)
				}
			}()
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
	// 	AllowOrigins: "*",
	// 	AllowHeaders: "Origin, Content-Type, Accept",
	// }))

	// WebSocket
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		hub.register <- c
		defer func() { hub.unregister <- c }()

		rows, err := db.Query(`SELECT id, title, description, link, source, category, 
			sentiment, published_at, translated_title 
			FROM news ORDER BY published_at DESC LIMIT 50`)
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

	// API: لیست اخبار
	app.Get("/api/news", func(c fiber.Ctx) error {
		category := c.Query("category", "")
		limit := fiber.Query[int](c, "limit", 100)

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

	// API: جزئیات خبر
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

	// API: دسته‌بندی‌ها
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

	// API: به‌روزرسانی دستی
	app.Post("/api/refresh", func(c fiber.Ctx) error {
		go fetchAllFeeds()
		return c.JSON(fiber.Map{"message": "در حال به‌روزرسانی..."})
	})

	// API: آمار
	app.Get("/api/stats", func(c fiber.Ctx) error {
		var total, translated int
		db.QueryRow("SELECT COUNT(*) FROM news").Scan(&total)
		db.QueryRow("SELECT COUNT(*) FROM news WHERE translated_content != '' AND translated_content != 'ترجمه در دسترس نیست'").Scan(&translated)
		return c.JSON(fiber.Map{
			"total":      total,
			"translated": translated,
			"pending":    total - translated,
		})
	})

	log.Println("🚀 سرور روی http://localhost:3000 اجرا شد")
	log.Fatal(app.Listen(":3000"))
}
