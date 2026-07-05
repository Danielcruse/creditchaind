// Minimal, self-contained QR code encoder - vanilla JS, no CDN/third-
// party dependency (consistent with this app's "plain HTML/CSS/JS"
// architecture, same reasoning as using the browser's native
// Web Crypto for signing instead of a vendored crypto library).
//
// Fixed to QR Version 5 (37x37 modules), error correction level L,
// byte mode only - comfortably fits a 40-character hex address plus a
// short label, which is the only thing this app ever encodes. This is
// NOT a general-purpose QR library (no version auto-selection, no
// alphanumeric/kanji modes, no multi-block interleaving) - just
// enough of the spec to reliably encode a short ASCII string.
const QR = (function () {
    const VERSION = 5;
    const SIZE = 17 + 4 * VERSION; // 37
    const DATA_CODEWORDS = 108;
    const EC_CODEWORDS = 26;
    const TOTAL_CODEWORDS = DATA_CODEWORDS + EC_CODEWORDS;

    // --- GF(256) arithmetic (primitive polynomial 0x11D, generator 2) ---
    const EXP = new Uint8Array(512);
    const LOG = new Uint8Array(256);
    (function buildGF() {
        let x = 1;
        for (let i = 0; i < 255; i++) {
            EXP[i] = x;
            LOG[x] = i;
            x <<= 1;
            if (x & 0x100) x ^= 0x11d;
        }
        for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
    })();

    function gfMul(a, b) {
        if (a === 0 || b === 0) return 0;
        return EXP[LOG[a] + LOG[b]];
    }

    // Reed-Solomon generator polynomial for EC_CODEWORDS codewords.
    function generatorPoly(degree) {
        let poly = [1];
        for (let i = 0; i < degree; i++) {
            const next = new Array(poly.length + 1).fill(0);
            for (let j = 0; j < poly.length; j++) {
                next[j] ^= gfMul(poly[j], EXP[i]);
                next[j + 1] ^= poly[j];
            }
            poly = next;
        }
        return poly;
    }

    function computeECCodewords(dataBytes) {
        const gen = generatorPoly(EC_CODEWORDS);
        const res = dataBytes.slice();
        for (let i = 0; i < DATA_CODEWORDS; i++) {
            res.push(0);
        }
        for (let i = 0; i < DATA_CODEWORDS; i++) {
            const coef = res[i];
            if (coef === 0) continue;
            for (let j = 0; j < gen.length; j++) {
                res[i + j] ^= gfMul(gen[j], coef);
            }
        }
        return res.slice(DATA_CODEWORDS, DATA_CODEWORDS + EC_CODEWORDS);
    }

    // --- Data encoding: byte mode only ---
    function encodeData(text) {
        const bytes = Array.from(new TextEncoder().encode(text));
        if (bytes.length > DATA_CODEWORDS - 3) {
            // Truncate defensively - every real caller in this app
            // passes a fixed-format short string well under this
            // limit, so this only guards against a future misuse.
            bytes.length = DATA_CODEWORDS - 3;
        }

        const bits = [];
        const pushBits = (value, len) => {
            for (let i = len - 1; i >= 0; i--) bits.push((value >> i) & 1);
        };

        pushBits(0b0100, 4); // byte mode indicator
        pushBits(bytes.length, 8); // character count (8 bits for versions 1-9)
        for (const b of bytes) pushBits(b, 8);

        // Terminator (up to 4 bits) then pad to a byte boundary.
        for (let i = 0; i < 4 && bits.length < DATA_CODEWORDS * 8; i++) bits.push(0);
        while (bits.length % 8 !== 0) bits.push(0);

        const codewords = [];
        for (let i = 0; i < bits.length; i += 8) {
            let byte = 0;
            for (let j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j];
            codewords.push(byte);
        }

        // Pad with the standard alternating pad codewords until full.
        const padBytes = [0xec, 0x11];
        let p = 0;
        while (codewords.length < DATA_CODEWORDS) {
            codewords.push(padBytes[p % 2]);
            p++;
        }

        return codewords;
    }

    // --- Matrix construction ---
    function isFinderArea(r, c) {
        const inTopLeft = r < 9 && c < 9;
        const inTopRight = r < 9 && c >= SIZE - 8;
        const inBottomLeft = r >= SIZE - 8 && c < 9;
        return inTopLeft || inTopRight || inBottomLeft;
    }

    // Version 5's single alignment pattern center (spec table: [6, 30]
    // for version 5, excluding the corner already covered by a finder
    // pattern).
    const ALIGNMENT_CENTERS = [6, 30];

    function isAlignmentArea(r, c) {
        for (const cr of ALIGNMENT_CENTERS) {
            for (const cc of ALIGNMENT_CENTERS) {
                if (cr === 6 && cc === 6) continue; // overlaps top-left finder
                if (Math.abs(r - cr) <= 2 && Math.abs(c - cc) <= 2) return true;
            }
        }
        return false;
    }

    function isTimingArea(r, c) {
        return r === 6 || c === 6;
    }

    function isFormatArea(r, c) {
        if (r === 8 && (c <= 8 || c >= SIZE - 8)) return true;
        if (c === 8 && (r <= 8 || r >= SIZE - 7)) return true;
        return false;
    }

    function isFunctionModule(r, c) {
        return isFinderArea(r, c) || isAlignmentArea(r, c) || isTimingArea(r, c) || isFormatArea(r, c);
    }

    function drawFinder(matrix, r0, c0) {
        for (let r = -1; r <= 7; r++) {
            for (let c = -1; c <= 7; c++) {
                const rr = r0 + r;
                const cc = c0 + c;
                if (rr < 0 || cc < 0 || rr >= SIZE || cc >= SIZE) continue;
                const onRing = r === -1 || r === 7 || c === -1 || c === 7;
                const onInner = r >= 0 && r <= 6 && c >= 0 && c <= 6 && (r === 0 || r === 6 || c === 0 || c === 6);
                const onCore = r >= 2 && r <= 4 && c >= 2 && c <= 4;
                matrix[rr][cc] = onRing ? 0 : onInner ? 1 : onCore ? 1 : 0;
            }
        }
    }

    function drawAlignment(matrix, cr, cc) {
        for (let r = -2; r <= 2; r++) {
            for (let c = -2; c <= 2; c++) {
                const onRing = r === -2 || r === 2 || c === -2 || c === 2;
                const onCenter = r === 0 && c === 0;
                matrix[cr + r][cc + c] = onRing || onCenter ? 1 : 0;
            }
        }
    }

    // BCH(15,5) format info, generator 0x537, XOR mask 0x5412 - fixed
    // formula from the QR spec, independent of version.
    function formatBits(errorLevelBits, maskPattern) {
        const data = (errorLevelBits << 3) | maskPattern;
        let d = data << 10;
        const gen = 0b10100110111;
        for (let i = 4; i >= 0; i--) {
            if (d & (1 << (i + 10))) {
                d ^= gen << i;
            }
        }
        const bits = (data << 10) | d;
        return bits ^ 0x5412;
    }

    function buildMatrix(dataCodewords) {
        const matrix = Array.from({ length: SIZE }, () => new Array(SIZE).fill(null));

        drawFinder(matrix, 0, 0);
        drawFinder(matrix, 0, SIZE - 7);
        drawFinder(matrix, SIZE - 7, 0);

        for (const cr of ALIGNMENT_CENTERS) {
            for (const cc of ALIGNMENT_CENTERS) {
                if (cr === 6 && cc === 6) continue;
                drawAlignment(matrix, cr, cc);
            }
        }

        for (let i = 0; i < SIZE; i++) {
            if (matrix[6][i] === null) matrix[6][i] = i % 2 === 0 ? 1 : 0;
            if (matrix[i][6] === null) matrix[i][6] = i % 2 === 0 ? 1 : 0;
        }

        matrix[SIZE - 8][8] = 1; // dark module (fixed, version-independent position rule)

        // Convert codewords to a bit stream, MSB first.
        const bits = [];
        for (const byte of dataCodewords) {
            for (let i = 7; i >= 0; i--) bits.push((byte >> i) & 1);
        }

        // Zigzag placement: column pairs from the right edge, skipping
        // the vertical timing column, alternating scan direction.
        const MASK = 0; // mask pattern 0: (row + col) % 2 === 0
        let bitIndex = 0;
        let upward = true;
        for (let colPair = SIZE - 1; colPair > 0; colPair -= 2) {
            const col = colPair === 6 ? colPair - 1 : colPair; // skip timing column
            for (let i = 0; i < SIZE; i++) {
                const row = upward ? SIZE - 1 - i : i;
                for (const c of [col, col - 1]) {
                    if (matrix[row][c] !== null) continue; // function module, skip
                    let bit = bitIndex < bits.length ? bits[bitIndex] : 0;
                    bitIndex++;
                    const maskApply = (row + c) % 2 === 0;
                    if (maskApply) bit ^= 1;
                    matrix[row][c] = bit;
                }
            }
            upward = !upward;
        }

        // Format info (error correction level L = binary 01) in both
        // reserved locations around the top-left finder.
        const fBits = formatBits(0b01, MASK);
        const fmt = [];
        for (let i = 14; i >= 0; i--) fmt.push((fBits >> i) & 1);

        for (let i = 0; i <= 5; i++) matrix[8][i] = fmt[i];
        matrix[8][7] = fmt[6];
        matrix[8][8] = fmt[7];
        matrix[7][8] = fmt[8];
        for (let i = 9; i <= 14; i++) matrix[14 - i][8] = fmt[i];

        for (let i = 0; i <= 7; i++) matrix[SIZE - 1 - i][8] = fmt[i];
        matrix[8][SIZE - 8] = fmt[7];
        for (let i = 8; i <= 14; i++) matrix[8][SIZE - 15 + i] = fmt[i];

        for (let r = 0; r < SIZE; r++) {
            for (let c = 0; c < SIZE; c++) {
                if (matrix[r][c] === null) matrix[r][c] = 0;
            }
        }

        return matrix;
    }

    function encode(text) {
        const dataCodewords = encodeData(text);
        const ecCodewords = computeECCodewords(dataCodewords);
        const allCodewords = dataCodewords.concat(ecCodewords);
        return buildMatrix(allCodewords);
    }

    // Renders the matrix into an existing <canvas> element.
    function renderToCanvas(canvas, text, moduleSize) {
        moduleSize = moduleSize || 6;
        const matrix = encode(text);
        const quiet = 4; // quiet-zone modules, per spec minimum
        const dim = (SIZE + quiet * 2) * moduleSize;
        canvas.width = dim;
        canvas.height = dim;

        const ctx = canvas.getContext("2d");
        ctx.fillStyle = "#ffffff";
        ctx.fillRect(0, 0, dim, dim);
        ctx.fillStyle = "#000000";
        for (let r = 0; r < SIZE; r++) {
            for (let c = 0; c < SIZE; c++) {
                if (matrix[r][c]) {
                    ctx.fillRect((c + quiet) * moduleSize, (r + quiet) * moduleSize, moduleSize, moduleSize);
                }
            }
        }
    }

    return { encode, renderToCanvas, SIZE };
})();
