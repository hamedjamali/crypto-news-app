import { useState, useEffect, useCallback } from 'react'
import { useWebSocket } from './useWebSocket'
import NewsDetail from './NewsDetail'

const API = '/api'
const WS_URL = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`

const categoryColors = {
    'بیت‌کوین': '#f7931a',
    'اتریوم': '#627eea',
    'آلت‌کوین‌ها': '#8247e5',
    'مقررات': '#e74c3c',
    'دیفای': '#00d395',
    'ETF و نهادی': '#3498db',
    'تکنولوژی': '#9b59b6',
    'عمومی': '#95a5a6',
}

const sentimentLabels = {
    positive: { text: '📈', color: '#238636' },
    negative: { text: '📉', color: '#da3633' },
    neutral: { text: '⚖️', color: '#6e7681' },
}

function App() {
    const [news, setNews] = useState([])
    const [categories, setCategories] = useState(['همه'])
    const [activeCategory, setActiveCategory] = useState('همه')
    const [loading, setLoading] = useState(true)
    const [lastUpdate, setLastUpdate] = useState(null)
    const [newBadge, setNewBadge] = useState(false)
    const [selectedNews, setSelectedNews] = useState(null)

    const { isConnected, lastMessage } = useWebSocket(WS_URL)

    // پیام‌های WebSocket
    useEffect(() => {
        if (!lastMessage) return

        if (lastMessage.type === 'initial_news') {
            setNews(lastMessage.data)
            setLoading(false)
        } else if (lastMessage.type === 'new_news') {
            const newItem = lastMessage.data
            setNews(prev => {
                if (prev.some(n => n.link === newItem.link)) return prev
                return [newItem, ...prev]
            })
            setNewBadge(true)
            setTimeout(() => setNewBadge(false), 3000)
        }
    }, [lastMessage])

    // دسته‌بندی‌ها
    useEffect(() => {
        fetch(`${API}/categories`)
            .then(res => res.json())
            .then(data => setCategories(data))
            .catch(console.error)
    }, [news.length])

    // بارگذاری اولیه از REST
    const fetchNews = useCallback(async () => {
        setLoading(true)
        try {
            const res = await fetch(`${API}/news?limit=100`)
            const data = await res.json()
            setNews(data)
            setLastUpdate(new Date().toLocaleTimeString('fa-IR'))
        } catch (err) {
            console.error(err)
        } finally {
            setLoading(false)
        }
    }, [])

    useEffect(() => {
        if (news.length === 0) fetchNews()
    }, [fetchNews])

    const handleRefresh = async () => {
        await fetch(`${API}/refresh`, { method: 'POST' })
        setTimeout(fetchNews, 3000)
    }

    const filteredNews = activeCategory === 'همه'
        ? news
        : news.filter(n => n.category === activeCategory)

    const timeAgo = (dateStr) => {
        const diff = Math.floor((Date.now() - new Date(dateStr)) / 1000)
        if (diff < 60) return 'همین الان'
        if (diff < 3600) return `${Math.floor(diff / 60)} دقیقه پیش`
        if (diff < 86400) return `${Math.floor(diff / 3600)} ساعت پیش`
        return `${Math.floor(diff / 86400)} روز پیش`
    }

    return (
        <div style={styles.container}>
            <header style={styles.header}>
                <div style={styles.headerContent}>
                    <h1 style={styles.title}>📰 اخبار زنده کریپتو</h1>
                    <div style={styles.headerRight}>
                        <span style={{
                            ...styles.wsStatus,
                            background: isConnected ? '#238636' : '#da3633',
                        }}>
                            {isConnected ? '● متصل' : '○ قطع'}
                        </span>
                        {lastUpdate && (
                            <span style={styles.updateTime}>به‌روزرسانی: {lastUpdate}</span>
                        )}
                        <button onClick={handleRefresh} style={styles.refreshBtn}>
                            🔄 به‌روزرسانی
                        </button>
                    </div>
                </div>
            </header>

            <div style={styles.filters}>
                {categories.map(cat => (
                    <button
                        key={cat}
                        onClick={() => setActiveCategory(cat)}
                        style={{
                            ...styles.filterBtn,
                            ...(activeCategory === cat ? styles.filterBtnActive : {}),
                            borderColor: categoryColors[cat] || '#555',
                        }}
                    >
                        {cat}
                    </button>
                ))}
            </div>

            {newBadge && <div style={styles.newBadge}>🔔 خبر جدید دریافت شد!</div>}

            {loading && news.length === 0 ? (
                <div style={styles.loading}>در حال بارگذاری...</div>
            ) : filteredNews.length === 0 ? (
                <div style={styles.empty}>خبری یافت نشد</div>
            ) : (
                <div style={styles.newsGrid}>
                    {filteredNews.map(item => {
                        const sent = sentimentLabels[item.sentiment] || sentimentLabels.neutral
                        return (
                            <div
                                key={item.id || item.link}
                                onClick={() => setSelectedNews(item)}
                                style={styles.card}
                            >
                                <div style={styles.cardHeader}>
                                    <span style={{
                                        ...styles.badge,
                                        background: categoryColors[item.category] || '#555',
                                    }}>
                                        {item.category}
                                    </span>
                                    <span style={{ ...styles.sentBadge, background: sent.color }}>
                                        {sent.text}
                                    </span>
                                    <span style={styles.source}>{item.source}</span>
                                </div>
                                <h3 style={styles.cardTitle}>
                                    {item.translated_title || item.title}
                                </h3>
                                <p style={styles.cardDesc}>
                                    {item.description?.replace(/<[^>]*>/g, '').slice(0, 160)}...
                                </p>
                                <div style={styles.cardFooter}>
                                    <span>{timeAgo(item.published_at)}</span>
                                    <span style={styles.readMore}>📖 خواندن کامل</span>
                                </div>
                            </div>
                        )
                    })}
                </div>
            )}

            {selectedNews && (
                <NewsDetail news={selectedNews} onClose={() => setSelectedNews(null)} />
            )}
        </div>
    )
}

const styles = {
    container: {
        maxWidth: 1200, margin: '0 auto', padding: '0 20px',
        fontFamily: 'Tahoma, Arial, sans-serif', direction: 'rtl',
        background: '#0d1117', minHeight: '100vh', color: '#e6edf3',
    },
    header: {
        padding: '24px 0', borderBottom: '1px solid #21262d', marginBottom: 20,
    },
    headerContent: {
        display: 'flex', justifyContent: 'space-between',
        alignItems: 'center', flexWrap: 'wrap', gap: 12,
    },
    headerRight: { display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' },
    title: { margin: 0, fontSize: 24, color: '#58a6ff' },
    wsStatus: {
        padding: '4px 12px', borderRadius: 20, fontSize: 12,
        color: '#fff', fontWeight: 'bold',
    },
    updateTime: { fontSize: 12, color: '#8b949e' },
    refreshBtn: {
        padding: '8px 16px', background: '#21262d', color: '#e6edf3',
        border: '1px solid #30363d', borderRadius: 6, cursor: 'pointer',
        fontSize: 13, fontFamily: 'inherit',
    },
    filters: {
        display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 24,
    },
    filterBtn: {
        padding: '6px 16px', background: 'transparent', color: '#8b949e',
        border: '1px solid #30363d', borderRadius: 20, cursor: 'pointer',
        fontSize: 13, fontFamily: 'inherit', transition: 'all 0.2s',
    },
    filterBtnActive: {
        background: '#21262d', color: '#e6edf3', fontWeight: 'bold',
    },
    newBadge: {
        background: '#238636', color: '#fff', padding: '8px 16px',
        borderRadius: 6, marginBottom: 16, textAlign: 'center',
        fontSize: 14, fontWeight: 'bold',
    },
    newsGrid: {
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))',
        gap: 16, paddingBottom: 40,
    },
    card: {
        background: '#161b22', border: '1px solid #21262d', borderRadius: 10,
        padding: 18, cursor: 'pointer',
        transition: 'border-color 0.2s, transform 0.15s',
        display: 'flex', flexDirection: 'column',
    },
    cardHeader: {
        display: 'flex', justifyContent: 'space-between',
        alignItems: 'center', marginBottom: 10, gap: 6,
    },
    badge: {
        padding: '2px 10px', borderRadius: 12, fontSize: 11,
        color: '#fff', fontWeight: 'bold',
    },
    sentBadge: {
        padding: '2px 8px', borderRadius: 12, fontSize: 11,
        color: '#fff', fontWeight: 'bold',
    },
    source: { fontSize: 11, color: '#8b949e', marginRight: 'auto' },
    cardTitle: {
        margin: '0 0 8px', fontSize: 15, lineHeight: 1.6, color: '#e6edf3', overflowX: "auto",
    },
    cardDesc: {
        margin: 0, fontSize: 13, color: '#8b949e', lineHeight: 1.7, flex: 1,
    },
    cardFooter: {
        display: 'flex', justifyContent: 'space-between',
        marginTop: 12, fontSize: 12, color: '#58a6ff',
    },
    readMore: { fontWeight: 'bold' },
    loading: { textAlign: 'center', padding: 60, color: '#8b949e' },
    empty: { textAlign: 'center', padding: 60, color: '#8b949e' },
}

export default App
