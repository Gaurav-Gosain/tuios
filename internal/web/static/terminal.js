// TUIOS Web Terminal Client - Optimized with settings panel
(function() {
    'use strict';

    // Message types (must match server)
    const MSG_INPUT = 0x30;    // '0'
    const MSG_OUTPUT = 0x31;   // '1'
    const MSG_RESIZE = 0x32;   // '2'
    const MSG_PING = 0x33;     // '3'
    const MSG_PONG = 0x34;     // '4'
    const MSG_TITLE = 0x35;    // '5'
    const MSG_OPTIONS = 0x36;  // '6'
    const MSG_CLOSE = 0x37;    // '7'

    // Font family with fallbacks
    const FONT_FAMILY = "'JetBrainsMono Nerd Font Mono', 'JetBrains Mono', 'Fira Code', Menlo, Monaco, monospace";

    // Settings storage
    const STORAGE_KEY = 'tuios-web-settings';

    class TuiosTerminal {
        constructor() {
            this.term = null;
            this.fitAddon = null;
            this.webglAddon = null;
            this.canvasAddon = null;
            this.webLinksAddon = null;
            this.connected = false;
            this.readOnly = false;
            this.reconnectAttempts = 0;
            this.maxReconnectAttempts = 5;
            this.reconnectDelay = 1000;
            this.pingInterval = null;
            this.encoder = new TextEncoder();
            this.decoder = new TextDecoder();
            this.useWebTransport = false;
            this.wsConnection = null;
            this.wtTransport = null;
            this.wtWriter = null;
            this.wtReader = null;
            this.resizeTimeout = null;
            
            // Performance: Pre-allocate reusable buffers
            this.sendBuffer = new Uint8Array(1024);
            this.frameBuffer = new Uint8Array(1028);
            this.pingBuffer = new Uint8Array([MSG_PING]); // Pre-allocated ping
            this.writeBuffer = new Uint8Array(64 * 1024); // Pre-allocated for combining writes
            
            // Performance: Batch terminal writes with requestAnimationFrame
            this.pendingWrites = [];
            this.writeScheduled = false;
            
            // Performance: Mouse event deduplication
            this.lastMouseCell = { col: -1, row: -1, button: -1 };
            this.mouseEventsFiltered = 0;
            this.mouseEventsSent = 0;
            
            // Settings
            this.settings = this.loadSettings();
            this.currentRenderer = 'unknown';
            this.currentTransport = 'unknown';
            
            // Cached DOM elements (set in init)
            this.statusEl = null;
            this.statusTextEl = null;
        }

        loadSettings() {
            try {
                const saved = localStorage.getItem(STORAGE_KEY);
                if (saved) {
                    return JSON.parse(saved);
                }
            } catch (e) {}
            return {
                transport: 'auto',
                renderer: 'auto',
                fontSize: 14
            };
        }

        saveSettings() {
            try {
                localStorage.setItem(STORAGE_KEY, JSON.stringify(this.settings));
            } catch (e) {}
        }

        getTerminalOptions() {
            return {
                fontFamily: FONT_FAMILY,
                fontSize: this.settings.fontSize,
                fontWeight: 'normal',
                fontWeightBold: 'bold',
                lineHeight: 1.0,
                letterSpacing: 0,
                cursorBlink: true,
                cursorStyle: 'block',
                cursorInactiveStyle: 'outline',
                scrollback: 5000,
                tabStopWidth: 8,
                allowProposedApi: true,
                allowTransparency: false,
                smoothScrollDuration: 0,
                macOptionIsMeta: true,
                macOptionClickForcesSelection: true,
                rightClickSelectsWord: true,
                drawBoldTextInBrightColors: false,
                fastScrollModifier: 'alt',
                fastScrollSensitivity: 5,
                minimumContrastRatio: 1,
                theme: {
                    foreground: '#cdd6f4',
                    background: '#1e1e2e',
                    cursor: '#f5e0dc',
                    cursorAccent: '#1e1e2e',
                    selectionBackground: '#585b70',
                    selectionForeground: '#cdd6f4',
                    selectionInactiveBackground: '#45475a',
                    black: '#45475a',
                    red: '#f38ba8',
                    green: '#a6e3a1',
                    yellow: '#f9e2af',
                    blue: '#89b4fa',
                    magenta: '#f5c2e7',
                    cyan: '#94e2d5',
                    white: '#bac2de',
                    brightBlack: '#585b70',
                    brightRed: '#f38ba8',
                    brightGreen: '#a6e3a1',
                    brightYellow: '#f9e2af',
                    brightBlue: '#89b4fa',
                    brightMagenta: '#f5c2e7',
                    brightCyan: '#94e2d5',
                    brightWhite: '#a6adc8'
                }
            };
        }

        async init() {
            // Cache DOM elements early
            this.statusEl = document.getElementById('connection-status');
            this.statusTextEl = document.getElementById('status-text');
            
            await this.loadFonts();
            this.updateStatus('connecting', 'Initializing terminal...');

            this.term = new Terminal(this.getTerminalOptions());
            
            this.fitAddon = new FitAddon.FitAddon();
            this.term.loadAddon(this.fitAddon);

            const container = document.getElementById('terminal');
            this.term.open(container);

            // Initialize renderer based on settings
            await new Promise(resolve => setTimeout(() => this.initRenderer().then(resolve), 100));

            // Load web links addon
            try {
                this.webLinksAddon = new WebLinksAddon.WebLinksAddon();
                this.term.loadAddon(this.webLinksAddon);
            } catch (e) {}

            this.fitAddon.fit();

            // Handle terminal input
            this.term.onData(data => {
                if (!this.readOnly && this.connected) {
                    this.sendInput(data);
                }
            });

            // Handle binary input (mouse events) with cell-based deduplication
            this.term.onBinary(data => {
                if (!this.readOnly && this.connected) {
                    this.sendMouseEvent(data);
                }
            });

            // Handle resize with debouncing
            const resizeObserver = new ResizeObserver(() => this.handleResize());
            resizeObserver.observe(container);
            window.addEventListener('resize', () => this.handleResize(), { passive: true });

            // Setup settings panel
            this.setupSettingsPanel();

            await this.connect();
            this.term.focus();

            container.addEventListener('contextmenu', e => e.preventDefault());
        }

        // Parse SGR mouse escape sequence and extract cell coordinates
        parseMouseEvent(data) {
            if (data.length < 6) return null;
            
            if (data.charCodeAt(0) !== 0x1b || 
                data.charCodeAt(1) !== 0x5b || 
                data.charCodeAt(2) !== 0x3c) {
                return null;
            }
            
            const rest = data.substring(3);
            const terminator = rest[rest.length - 1];
            
            if (terminator !== 'M' && terminator !== 'm') {
                return null;
            }
            
            const parts = rest.substring(0, rest.length - 1).split(';');
            if (parts.length !== 3) return null;
            
            const button = parseInt(parts[0], 10);
            const col = parseInt(parts[1], 10);
            const row = parseInt(parts[2], 10);
            
            if (isNaN(button) || isNaN(col) || isNaN(row)) return null;
            
            const isMotion = (button & 32) !== 0;
            const isRelease = terminator === 'm';
            
            return { button, col, row, isMotion, isRelease };
        }

        async sendMouseEvent(data) {
            const parsed = this.parseMouseEvent(data);
            
            if (parsed) {
                if (parsed.isMotion) {
                    if (parsed.col === this.lastMouseCell.col && 
                        parsed.row === this.lastMouseCell.row &&
                        parsed.button === this.lastMouseCell.button) {
                        this.mouseEventsFiltered++;
                        return;
                    }
                }
                
                this.lastMouseCell.col = parsed.col;
                this.lastMouseCell.row = parsed.row;
                this.lastMouseCell.button = parsed.button;
                
                if (parsed.isRelease) {
                    this.lastMouseCell = { col: -1, row: -1, button: -1 };
                }
                
                this.mouseEventsSent++;
            }
            
            await this.sendBinary(data);
        }

        setupSettingsPanel() {
            const toggle = document.getElementById('settings-toggle');
            const panel = document.getElementById('settings-panel');
            const apply = document.getElementById('settings-apply');
            const close = document.getElementById('settings-close');
            const transportSelect = document.getElementById('transport-select');
            const rendererSelect = document.getElementById('renderer-select');
            const fontSizeInput = document.getElementById('font-size');
            const fontSizeValue = document.getElementById('font-size-value');

            transportSelect.value = this.settings.transport;
            rendererSelect.value = this.settings.renderer;
            fontSizeInput.value = this.settings.fontSize;
            fontSizeValue.textContent = this.settings.fontSize + 'px';

            toggle.addEventListener('click', () => {
                panel.classList.toggle('hidden');
                this.updateSettingsInfo();
            });

            close.addEventListener('click', () => {
                panel.classList.add('hidden');
            });

            fontSizeInput.addEventListener('input', () => {
                fontSizeValue.textContent = fontSizeInput.value + 'px';
            }, { passive: true });

            apply.addEventListener('click', async () => {
                this.settings.transport = transportSelect.value;
                this.settings.renderer = rendererSelect.value;
                this.settings.fontSize = parseInt(fontSizeInput.value);
                this.saveSettings();
                
                this.term.options.fontSize = this.settings.fontSize;
                this.fitAddon.fit();
                
                panel.classList.add('hidden');
                await this.reconnect();
            });
        }

        updateSettingsInfo() {
            const rendererInfo = document.getElementById('renderer-info');
            const transportInfo = document.getElementById('transport-info');
            
            rendererInfo.textContent = `Renderer: ${this.currentRenderer}`;
            transportInfo.textContent = `Transport: ${this.currentTransport}`;
            
            if (this.mouseEventsSent > 0 || this.mouseEventsFiltered > 0) {
                const total = this.mouseEventsSent + this.mouseEventsFiltered;
                const pct = total > 0 ? Math.round(this.mouseEventsFiltered / total * 100) : 0;
                transportInfo.textContent += ` | Mouse: ${pct}% filtered`;
            }
        }

        async loadFonts() {
            this.updateStatus('connecting', 'Loading fonts...');

            const fonts = [
                new FontFace('JetBrainsMono Nerd Font Mono', 'url(/static/fonts/JetBrainsMonoNerdFontMono-Regular.ttf)', { weight: '400', style: 'normal' }),
                new FontFace('JetBrainsMono Nerd Font Mono', 'url(/static/fonts/JetBrainsMonoNerdFontMono-Bold.ttf)', { weight: '700', style: 'normal' }),
                new FontFace('JetBrainsMono Nerd Font Mono', 'url(/static/fonts/JetBrainsMonoNerdFontMono-Italic.ttf)', { weight: '400', style: 'italic' }),
                new FontFace('JetBrainsMono Nerd Font Mono', 'url(/static/fonts/JetBrainsMonoNerdFontMono-BoldItalic.ttf)', { weight: '700', style: 'italic' }),
            ];

            try {
                const loadedFonts = await Promise.all(fonts.map(font => font.load()));
                loadedFonts.forEach(font => document.fonts.add(font));
                await document.fonts.ready;
            } catch (e) {
                console.warn('Font loading failed:', e);
            }
        }

        async initRenderer() {
            const preference = this.settings.renderer;
            
            if (preference === 'dom') {
                this.currentRenderer = 'DOM';
                return;
            }
            
            if (preference === 'webgl' || preference === 'auto') {
                if (await this.tryWebGL()) return;
            }
            
            if (preference === 'canvas' || preference === 'auto') {
                if (this.tryCanvas()) return;
            }
            
            this.currentRenderer = 'DOM';
        }

        async tryWebGL() {
            try {
                if (!this.term || !this.term.element) return false;

                const testCanvas = document.createElement('canvas');
                const gl = testCanvas.getContext('webgl2') || testCanvas.getContext('webgl');
                if (!gl) return false;

                this.webglAddon = new WebglAddon.WebglAddon();
                this.webglAddon.onContextLoss(() => {
                    console.log('WebGL context lost');
                    this.webglAddon.dispose();
                    this.webglAddon = null;
                    this.tryCanvas();
                });

                this.term.loadAddon(this.webglAddon);
                this.currentRenderer = 'WebGL';
                return true;
            } catch (e) {
                if (this.webglAddon) {
                    try { this.webglAddon.dispose(); } catch (_) {}
                    this.webglAddon = null;
                }
                return false;
            }
        }

        tryCanvas() {
            try {
                if (typeof CanvasAddon !== 'undefined' && CanvasAddon.CanvasAddon) {
                    this.canvasAddon = new CanvasAddon.CanvasAddon();
                    this.term.loadAddon(this.canvasAddon);
                    this.currentRenderer = 'Canvas';
                    return true;
                }
            } catch (e) {}
            return false;
        }

        async connect() {
            this.updateStatus('connecting', 'Connecting...');

            const preference = this.settings.transport;

            if ((preference === 'auto' || preference === 'webtransport') && typeof WebTransport !== 'undefined') {
                try {
                    await this.connectWebTransport();
                    return;
                } catch (e) {
                    console.log('WebTransport unavailable:', e.message);
                    if (preference === 'webtransport') {
                        this.updateStatus('disconnected', 'WebTransport failed');
                        return;
                    }
                }
            }

            if (preference !== 'webtransport') {
                await this.connectWebSocket();
            }
        }

        async reconnect() {
            this.handleDisconnect();
            this.reconnectAttempts = 0;
            await this.connect();
        }

        async connectWebTransport() {
            let transportOptions = {};
            let wtUrl = `https://127.0.0.1:${parseInt(window.location.port || '80') + 1}/webtransport`;
            
            try {
                const resp = await fetch('/cert-hash');
                if (resp.ok) {
                    const data = await resp.json();
                    if (data.wtUrl) wtUrl = data.wtUrl;
                    
                    const hashBytes = new Uint8Array(data.hashBytes);
                    transportOptions = {
                        serverCertificateHashes: [{
                            algorithm: 'sha-256',
                            value: hashBytes.buffer
                        }]
                    };
                }
            } catch (e) {}

            this.wtTransport = new WebTransport(wtUrl, transportOptions);

            this.wtTransport.closed
                .then(() => this.handleDisconnect())
                .catch(() => this.handleDisconnect());

            await this.wtTransport.ready;

            this.useWebTransport = true;
            this.connected = true;
            this.reconnectAttempts = 0;
            this.currentTransport = 'WebTransport (QUIC)';
            this.updateStatus('webtransport', 'Connected (QUIC)');

            const stream = await this.wtTransport.createBidirectionalStream();
            this.wtWriter = stream.writable.getWriter();
            this.wtReader = stream.readable.getReader();

            this.readWebTransportLoop();
            this.sendResize();
            this.startPing();
        }

        async connectWebSocket() {
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const url = `${protocol}//${window.location.host}/ws`;
            
            return new Promise((resolve, reject) => {
                this.wsConnection = new WebSocket(url);
                this.wsConnection.binaryType = 'arraybuffer';

                this.wsConnection.onopen = () => {
                    this.useWebTransport = false;
                    this.connected = true;
                    this.reconnectAttempts = 0;
                    this.currentTransport = 'WebSocket';
                    this.updateStatus('connected', 'Connected (WebSocket)');
                    this.sendResize();
                    this.startPing();
                    resolve();
                };

                this.wsConnection.onmessage = event => {
                    if (event.data instanceof ArrayBuffer) {
                        this.handleMessage(new Uint8Array(event.data));
                    }
                };

                this.wsConnection.onerror = reject;
                this.wsConnection.onclose = () => this.handleDisconnect();
            });
        }

        async readWebTransportLoop() {
            if (!this.useWebTransport || !this.wtReader) return;

            let buffer = new Uint8Array(64 * 1024);
            let bufferLen = 0;

            try {
                while (true) {
                    const { value, done } = await this.wtReader.read();
                    if (done) break;

                    if (bufferLen + value.length > buffer.length) {
                        const newBuffer = new Uint8Array(Math.max(buffer.length * 2, bufferLen + value.length));
                        newBuffer.set(buffer.subarray(0, bufferLen));
                        buffer = newBuffer;
                    }
                    
                    buffer.set(value, bufferLen);
                    bufferLen += value.length;

                    let offset = 0;
                    while (bufferLen - offset >= 4) {
                        const msgLen = (buffer[offset] << 24) | (buffer[offset + 1] << 16) | 
                                       (buffer[offset + 2] << 8) | buffer[offset + 3];

                        if (msgLen > 1024 * 1024) {
                            console.error('Message too large:', msgLen);
                            return;
                        }

                        if (bufferLen - offset < 4 + msgLen) break;

                        this.handleMessage(buffer.subarray(offset + 4, offset + 4 + msgLen));
                        offset += 4 + msgLen;
                    }

                    if (offset > 0) {
                        if (bufferLen > offset) {
                            buffer.copyWithin(0, offset, bufferLen);
                        }
                        bufferLen -= offset;
                    }
                }
            } catch (e) {
                if (this.connected) console.error('WebTransport read error:', e);
            }
        }

        handleMessage(data) {
            if (!data || data.length === 0) return;

            const msgType = data[0];

            switch (msgType) {
                case MSG_OUTPUT:
                    if (data.length > 1) {
                        this.scheduleWrite(data.subarray(1));
                    }
                    break;

                case MSG_CLOSE:
                    this.term.write('\r\n\x1b[33m[Session ended. Refresh to start new session.]\x1b[0m\r\n');
                    this.connected = false;
                    this.updateStatus('disconnected', 'Session ended');
                    break;

                case MSG_TITLE:
                    document.title = this.decoder.decode(data.subarray(1)) || 'TUIOS Web Terminal';
                    break;

                case MSG_OPTIONS:
                    try {
                        const options = JSON.parse(this.decoder.decode(data.subarray(1)));
                        this.readOnly = options.readOnly || false;
                        if (this.readOnly) {
                            this.updateStatus('connected', 'Connected (Read-Only)');
                        }
                    } catch (e) {}
                    break;

                case MSG_PONG:
                    break;
            }
        }

        // Batch terminal writes using requestAnimationFrame
        scheduleWrite(data) {
            this.pendingWrites.push(new Uint8Array(data));
            
            if (!this.writeScheduled) {
                this.writeScheduled = true;
                requestAnimationFrame(() => this.flushWrites());
            }
        }

        flushWrites() {
            this.writeScheduled = false;
            
            if (this.pendingWrites.length === 0) return;
            
            if (this.pendingWrites.length === 1) {
                this.term.write(this.pendingWrites[0]);
            } else {
                // Calculate total length
                let totalLen = 0;
                for (const w of this.pendingWrites) {
                    totalLen += w.length;
                }
                
                // Use pre-allocated buffer if it fits, otherwise allocate
                let combined;
                if (totalLen <= this.writeBuffer.length) {
                    combined = this.writeBuffer.subarray(0, totalLen);
                } else {
                    combined = new Uint8Array(totalLen);
                }
                
                let offset = 0;
                for (const w of this.pendingWrites) {
                    combined.set(w, offset);
                    offset += w.length;
                }
                
                this.term.write(combined);
            }
            
            // Reuse array instead of creating new one
            this.pendingWrites.length = 0;
        }

        async sendInput(data) {
            const encoded = this.encoder.encode(data);
            const len = encoded.length + 1;
            
            let msg;
            if (len <= this.sendBuffer.length) {
                msg = this.sendBuffer.subarray(0, len);
            } else {
                msg = new Uint8Array(len);
            }
            
            msg[0] = MSG_INPUT;
            msg.set(encoded, 1);
            await this.send(msg);
        }

        async sendBinary(data) {
            const len = data.length + 1;
            let msg;
            if (len <= this.sendBuffer.length) {
                msg = this.sendBuffer.subarray(0, len);
            } else {
                msg = new Uint8Array(len);
            }
            
            msg[0] = MSG_INPUT;
            for (let i = 0; i < data.length; i++) {
                msg[i + 1] = data.charCodeAt(i);
            }
            await this.send(msg);
        }

        async sendResize() {
            if (!this.term) return;
            
            const payload = this.encoder.encode(JSON.stringify({
                cols: this.term.cols,
                rows: this.term.rows
            }));
            const msg = new Uint8Array(payload.length + 1);
            msg[0] = MSG_RESIZE;
            msg.set(payload, 1);
            await this.send(msg);
        }

        async sendPing() {
            await this.send(this.pingBuffer);
        }

        async send(data) {
            if (!this.connected) return;

            try {
                if (this.useWebTransport && this.wtWriter) {
                    const frameLen = 4 + data.length;
                    let frame;
                    if (frameLen <= this.frameBuffer.length) {
                        frame = this.frameBuffer.subarray(0, frameLen);
                    } else {
                        frame = new Uint8Array(frameLen);
                    }
                    new DataView(frame.buffer, frame.byteOffset).setUint32(0, data.length);
                    frame.set(data, 4);
                    await this.wtWriter.write(frame);
                } else if (this.wsConnection && this.wsConnection.readyState === WebSocket.OPEN) {
                    this.wsConnection.send(data);
                }
            } catch (e) {
                console.error('Send error:', e);
            }
        }

        handleResize() {
            if (!this.fitAddon || !this.term) return;

            if (this.resizeTimeout) clearTimeout(this.resizeTimeout);

            this.resizeTimeout = setTimeout(() => {
                try {
                    this.fitAddon.fit();
                    if (this.connected) this.sendResize();
                } catch (e) {}
            }, 50);
        }

        handleDisconnect() {
            const wasConnected = this.connected;
            this.connected = false;
            
            if (this.pingInterval) {
                clearInterval(this.pingInterval);
                this.pingInterval = null;
            }

            if (this.useWebTransport) {
                if (this.wtWriter) { try { this.wtWriter.releaseLock(); } catch (_) {} this.wtWriter = null; }
                if (this.wtReader) { try { this.wtReader.releaseLock(); } catch (_) {} this.wtReader = null; }
                if (this.wtTransport) { try { this.wtTransport.close(); } catch (_) {} this.wtTransport = null; }
                this.useWebTransport = false;
            }

            if (this.wsConnection) {
                try { this.wsConnection.close(); } catch (_) {}
                this.wsConnection = null;
            }

            this.lastMouseCell = { col: -1, row: -1, button: -1 };

            if (!wasConnected) return;

            this.currentTransport = 'disconnected';
            this.updateStatus('disconnected', 'Disconnected');

            if (this.reconnectAttempts < this.maxReconnectAttempts) {
                this.reconnectAttempts++;
                const delay = this.reconnectDelay * Math.pow(1.5, this.reconnectAttempts - 1);
                this.updateStatus('connecting', `Reconnecting in ${Math.round(delay/1000)}s...`);
                setTimeout(() => this.connect(), delay);
            } else {
                this.updateStatus('disconnected', 'Connection lost');
                this.term.write('\r\n\x1b[31m[Connection lost. Refresh to reconnect.]\x1b[0m\r\n');
            }
        }

        startPing() {
            if (this.pingInterval) clearInterval(this.pingInterval);
            this.pingInterval = setInterval(() => {
                if (this.connected) this.sendPing();
            }, 30000);
        }

        updateStatus(status, text) {
            if (!this.statusEl || !this.statusTextEl) return;

            this.statusEl.className = status;
            this.statusTextEl.textContent = text;

            if (status === 'connected' || status === 'webtransport') {
                setTimeout(() => this.statusEl.classList.add('hidden'), 2000);
            } else {
                this.statusEl.classList.remove('hidden');
            }
        }
    }

    // Initialize
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', () => new TuiosTerminal().init().catch(console.error));
    } else {
        new TuiosTerminal().init().catch(console.error);
    }
})();
