import { useState, useEffect } from 'react'

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
    positive: { text: '📈 صعودی', color: '#238636' },
    negative: { text: '📉 نزولی', color: '#da3633' },
    neutral: { text: '⚖️ خنثی', color: '#6e7681' },
}

export default function NewsDetail({ news, onClose }) {
    const [detailed, setDetailed] = useState(null)
    const [loading, setLoading] = useState(true)
    const [showOriginal, setShowOriginal] = useState(false)

    useEffect(() => {
        if (!news?.id) return
        setLoading(true)
        fetch(`/api/news/${news.id}`)
            .then(res => res.json())
            .then(data => {
                setDetailed(data)
                setLoading(false)
            })
            .catch(err => {
                console.error(err)
                setLoading(false)
            })
    }, [news?.id])

    if (!news) return null

    const data = detailed || news
    const sentiment = sentimentLabels[data.sentiment] || sentimentLabels.neutral

    return (
        <div style={styles.overlay} onClick={onClose}>
            <div style={styles.modal} onClick={e => e.stopPropagation()}>
                <button onClick={onClose} style={styles.closeBtn}>✕</button>

                <div style={styles.header}>
                    <span style={{
                        ...styles.badge,
                        background: categoryColors[data.category] || '#555',
                    }}>
                        {data.category}
                    </span>
                    <span style={{ ...styles.badge, background: sentiment.color }}>
                        {sentiment.text}
                    </span>
                    <span style={styles.source}>{data.source}</span>
                </div>

                <h2 style={styles.title}>
                    {showOriginal ? data.title : (data.translated_title || data.title)}
                </h2>

                <div style={styles.actions}>
                    <button
                        onClick={() => setShowOriginal(!showOriginal)}
                        style={styles.toggleBtn}
                    >
                        {showOriginal ? '🇮🇷 نمایش ترجمه فارسی' : '🇬🇧 نمایش متن اصلی'}
                    </button>
                    <a
                        href={data.link}
                        target="_blank"
                        rel="noopener noreferrer"
                        style={styles.linkBtn}
                    >
                        ↗ مشاهده منبع اصلی
                    </a>
                </div>

                <div style={styles.content}>
                    {loading ? (
                        <div style={styles.loading}>⏳ در حال بارگذاری متن کامل...</div>
                    ) : showOriginal ? (
                        <p style={styles.text}>{data.full_content || data.description}</p>
                    ) : (
                        <p style={styles.text}>
                            {data.translated_content || 'ترجمه در دسترس نیست'}
                        </p>
                    )}
                </div>
            </div>
        </div>
    )
}

const styles = {
    overlay: {
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.85)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        zIndex: 1000, padding: 20,
    },
    modal: {
        background: '#161b22', borderRadius: 12, padding: 32,
        maxWidth: 850, width: '100%', maxHeight: '90vh', overflowY: 'auto',
        position: 'relative', border: '1px solid #30363d',
    },
    closeBtn: {
        position: 'absolute', top: 16, left: 16, background: 'transparent',
        border: 'none', color: '#8b949e', fontSize: 24, cursor: 'pointer',
        padding: 4, lineHeight: 1,
    },
    header: {
        display: 'flex', gap: 10, alignItems: 'center',
        marginBottom: 16, flexWrap: 'wrap',
    },
    badge: {
        padding: '4px 12px', borderRadius: 12, fontSize: 12,
        color: '#fff', fontWeight: 'bold',
    },
    source: { fontSize: 13, color: '#8b949e', marginRight: 'auto' },
    title: {
        fontSize: 22, lineHeight: 1.6, color: '#e6edf3',
        margin: '0 0 20px', paddingLeft: 24,
    },
    actions: {
        display: 'flex', gap: 12, marginBottom: 24, flexWrap: 'wrap',
    },
    toggleBtn: {
        padding: '10px 20px', background: '#21262d', color: '#e6edf3',
        border: '1px solid #30363d', borderRadius: 6, cursor: 'pointer',
        fontSize: 14, fontFamily: 'inherit',
    },
    linkBtn: {
        padding: '10px 20px', background: '#238636', color: '#fff',
        border: 'none', borderRadius: 6, cursor: 'pointer',
        fontSize: 14, textDecoration: 'none',
    },
    content: {
        lineHeight: 2, fontSize: 15, color: '#c9d1d9',
    },
    text: {
        whiteSpace: 'pre-wrap', margin: 0, textAlign: 'justify',
    },
    loading: {
        textAlign: 'center', padding: 40, color: '#8b949e',
    },
}