import { useState, useEffect, useRef, useCallback } from 'react'

export function useWebSocket(url) {
    const [isConnected, setIsConnected] = useState(false)
    const [lastMessage, setLastMessage] = useState(null)
    const wsRef = useRef(null)
    const reconnectAttempts = useRef(0)
    const reconnectTimeout = useRef(null)
    const isCleaningUp = useRef(false)
    const maxReconnectAttempts = 10

    const connect = useCallback(() => {
        // اگر در حال cleanup هستیم، وصل نشو
        if (isCleaningUp.current) return
        if (wsRef.current?.readyState === WebSocket.OPEN) return

        const ws = new WebSocket(url)
        wsRef.current = ws

        ws.onopen = () => {
            if (isCleaningUp.current) {
                ws.close()
                return
            }
            console.log('✅ WebSocket متصل شد')
            setIsConnected(true)
            reconnectAttempts.current = 0
        }

        ws.onmessage = (event) => {
            try {
                const data = JSON.parse(event.data)
                setLastMessage(data)
            } catch (err) {
                console.error('خطا در parse پیام:', err)
            }
        }

        ws.onclose = () => {
            if (isCleaningUp.current) return
            console.log('🔴 WebSocket قطع شد')
            setIsConnected(false)

            if (reconnectAttempts.current < maxReconnectAttempts) {
                const delay = Math.min(1000 * Math.pow(2, reconnectAttempts.current), 30000)
                reconnectAttempts.current += 1
                reconnectTimeout.current = setTimeout(connect, delay)
            }
        }

        ws.onerror = () => {
            // خطا رو نادیده بگیر (onclose خودش مدیریت می‌کنه)
        }
    }, [url])

    useEffect(() => {
        isCleaningUp.current = false
        connect()

        return () => {
            isCleaningUp.current = true
            if (reconnectTimeout.current) {
                clearTimeout(reconnectTimeout.current)
                reconnectTimeout.current = null
            }
            if (wsRef.current) {
                // اگر هنوز در حال اتصال است، onclose را نادیده بگیر
                wsRef.current.onclose = null
                wsRef.current.onerror = null
                if (wsRef.current.readyState === WebSocket.OPEN ||
                    wsRef.current.readyState === WebSocket.CONNECTING) {
                    wsRef.current.close()
                }
                wsRef.current = null
            }
        }
    }, [connect])

    return { isConnected, lastMessage }
}