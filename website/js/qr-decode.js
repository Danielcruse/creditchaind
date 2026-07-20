// qr-decode.js - a QR reader scoped EXACTLY to what website/js/qrcode.js
// produces: Version 5 (37x37 modules), error-correction level L, mask
// pattern 0, byte mode only. This is why the QR scanner never actually
// worked before: the previous implementation (in send.js/send-eth.js)
// gated entirely on the browser-native `BarcodeDetector` API, which
// only ships on Android Chrome/ChromeOS - it's absent on desktop
// Chrome, Edge, Firefox and Safari, so both the live-camera scan and
// the "take a photo" fallback silently failed for almost everyone.
//
// This is NOT a general-purpose QR decoder (no version auto-detection,
// no other EC levels/masks, no Reed-Solomon error correction for
// damaged/noisy scans) - same deliberate scope-narrowing qrcode.js's
// own header comment already applies to encoding. That's a reasonable
// trade here because the only thing this app ever asks a user to scan
// is another CreditChain wallet's receive-QR, which this app's own
// encoder generated with exactly these fixed parameters. Instead of
// implementing generic error correction, a scanned code's data
// codewords are verified by locally recomputing their expected EC
// codewords (via QR.computeECCodewords, the exact function the
// encoder itself uses) and requiring an exact match - a clean capture
// passes, a bad/blurry one is correctly rejected with "try again"
// rather than silently returning corrupted data.
const QRDecode = (function () {
    const SIZE = QR.SIZE; // 37
    const DATA_CODEWORDS = QR.DATA_CODEWORDS; // 108
    const EC_CODEWORDS = QR.EC_CODEWORDS; // 26
    const MASK = 0; // (row + col) % 2 === 0, matching qrcode.js's fixed mask

    // Finder pattern module-space centers, matching qrcode.js's
    // drawFinder(matrix, r0, c0) calls at (0,0), (0, SIZE-7), (SIZE-7, 0)
    // - each finder's dark square spans r0..r0+6, so its center is
    // r0+3, c0+3.
    const TL_MOD = { x: 3, y: 3 };
    const TR_MOD = { x: SIZE - 4, y: 3 };
    const BL_MOD = { x: 3, y: SIZE - 4 };

    // --- grayscale + Otsu binarization ---

    function grayscale(imageData) {
        const { data, width, height } = imageData;
        const gray = new Uint8ClampedArray(width * height);
        for (let i = 0, p = 0; i < data.length; i += 4, p++) {
            gray[p] = (data[i] * 299 + data[i + 1] * 587 + data[i + 2] * 114) / 1000;
        }
        return gray;
    }

    function otsuThreshold(gray) {
        const hist = new Array(256).fill(0);
        for (let i = 0; i < gray.length; i++) hist[gray[i]]++;
        const total = gray.length;
        let sum = 0;
        for (let t = 0; t < 256; t++) sum += t * hist[t];

        let sumB = 0;
        let wB = 0;
        let maxVar = -1;
        let threshold = 127;
        for (let t = 0; t < 256; t++) {
            wB += hist[t];
            if (wB === 0) continue;
            const wF = total - wB;
            if (wF === 0) break;
            sumB += t * hist[t];
            const mB = sumB / wB;
            const mF = (sum - sumB) / wF;
            const between = wB * wF * (mB - mF) * (mB - mF);
            if (between > maxVar) {
                maxVar = between;
                // The midpoint of the two class means, not the raw
                // split index t - for a near-pure two-tone image (no
                // anti-aliasing, e.g. a rendered QR canvas, or a very
                // sharp camera capture) the between-class variance is
                // identical across a whole run of t values, and using
                // t directly would lock onto the first tied index
                // (typically 0) instead of a threshold that actually
                // sits between the two clusters.
                threshold = (mB + mF) / 2;
            }
        }
        return threshold;
    }

    // 1 = dark module, 0 = light - matches qrcode.js's own matrix convention.
    function binarize(gray, threshold) {
        const bin = new Uint8Array(gray.length);
        for (let i = 0; i < gray.length; i++) bin[i] = gray[i] < threshold ? 1 : 0;
        return bin;
    }

    // --- finder pattern detection (1:1:3:1:1 dark:light:dark:light:dark ratio scan) ---

    function checkRatio(runs) {
        const total = runs[0] + runs[1] + runs[2] + runs[3] + runs[4];
        if (total < 7) return null;
        const unit = total / 7;
        const tol = unit * 0.6;
        if (Math.abs(runs[0] - unit) > tol) return null;
        if (Math.abs(runs[1] - unit) > tol) return null;
        if (Math.abs(runs[2] - unit * 3) > tol * 2) return null;
        if (Math.abs(runs[3] - unit) > tol) return null;
        if (Math.abs(runs[4] - unit) > tol) return null;
        return unit;
    }

    // Scans a single row of `bin` and returns candidate {x, moduleSize} centers.
    function scanLineForFinders(bin, width, rowOffset, stride, length) {
        const candidates = [];
        const runs = [0, 0, 0, 0, 0];
        let runCount = 0;
        let color = bin[rowOffset]; // 0 or 1
        let runStart = 0;

        const flush = (endPos) => {
            const runLen = endPos - runStart;
            if (runCount < 5) {
                for (let i = 0; i < 4; i++) runs[i] = runs[i + 1];
                runs[4] = runLen;
                runCount++;
            } else {
                for (let i = 0; i < 4; i++) runs[i] = runs[i + 1];
                runs[4] = runLen;
            }
            // A completed dark run (color about to flip from 1 to 0
            // would be checked on the NEXT flush) - we check right
            // after a run finishes if that run was dark (color===1),
            // since runs[4] just became that dark run's length and
            // runs[0..3] alternate light/dark/light/dark before it,
            // i.e. runs=[dark,light,dark,light,dark].
            if (runCount >= 5 && color === 1) {
                const unit = checkRatio(runs);
                if (unit !== null) {
                    const centerOffset = runs[4] + runs[3] + runs[2] / 2;
                    const centerPos = endPos - centerOffset;
                    candidates.push({ pos: centerPos, moduleSize: unit });
                }
            }
        };

        for (let i = 1; i < length; i++) {
            const px = bin[rowOffset + i * stride];
            if (px !== color) {
                flush(i);
                runStart = i;
                color = px;
            }
        }
        flush(length);
        return candidates;
    }

    // Re-checks a horizontal candidate against the perpendicular
    // (vertical) line through it to reject false positives and refine
    // the center estimate - standard cross-check technique.
    function crossCheckVertical(bin, width, height, cx, cy, moduleSize) {
        const cxi = Math.round(cx);
        if (cxi < 0 || cxi >= width) return null;
        const maxScan = Math.round(moduleSize * 8);
        const top = Math.max(0, Math.round(cy) - maxScan);
        const bottom = Math.min(height, Math.round(cy) + maxScan);
        const candidates = scanLineForFinders(bin, width, top * width + cxi, width, bottom - top);
        if (candidates.length === 0) return null;
        // pick the candidate whose center is closest to cy
        let best = null;
        let bestDist = Infinity;
        for (const c of candidates) {
            const y = top + c.pos;
            const d = Math.abs(y - cy);
            if (d < bestDist) {
                bestDist = d;
                best = { y, moduleSize: c.moduleSize };
            }
        }
        if (bestDist > moduleSize * 2) return null;
        return best;
    }

    function findFinderPatterns(bin, width, height) {
        const rawCandidates = [];
        for (let y = 0; y < height; y++) {
            const rowCandidates = scanLineForFinders(bin, width, y * width, 1, width);
            for (const c of rowCandidates) {
                const refined = crossCheckVertical(bin, width, height, c.pos, y, c.moduleSize);
                if (refined) {
                    rawCandidates.push({ x: c.pos, y: refined.y, moduleSize: (c.moduleSize + refined.moduleSize) / 2 });
                }
            }
        }
        if (rawCandidates.length === 0) return null;

        // Cluster nearby candidates (within ~2 module widths) - a real
        // finder pattern gets hit by several consecutive scan rows.
        const clusters = [];
        for (const c of rawCandidates) {
            let cluster = clusters.find((cl) => Math.abs(cl.x / cl.count - c.x) < c.moduleSize * 2 && Math.abs(cl.y / cl.count - c.y) < c.moduleSize * 2);
            if (!cluster) {
                cluster = { x: 0, y: 0, moduleSize: 0, count: 0 };
                clusters.push(cluster);
            }
            cluster.x += c.x;
            cluster.y += c.y;
            cluster.moduleSize += c.moduleSize;
            cluster.count++;
        }
        clusters.sort((a, b) => b.count - a.count);
        if (clusters.length < 3) return null;

        const top3 = clusters.slice(0, 3).map((cl) => ({
            x: cl.x / cl.count,
            y: cl.y / cl.count,
            moduleSize: cl.moduleSize / cl.count,
        }));
        return top3;
    }

    // Identifies which of the 3 finder centers is top-left, top-right,
    // bottom-left - works for any rotation angle, not just multiples
    // of 90 degrees, since it's based on angle/cross-product geometry
    // rather than absolute screen position.
    function orientFinders(points) {
        const dist2 = (a, b) => (a.x - b.x) * (a.x - b.x) + (a.y - b.y) * (a.y - b.y);
        const d01 = dist2(points[0], points[1]);
        const d02 = dist2(points[0], points[2]);
        const d12 = dist2(points[1], points[2]);

        // The top-left point is the one NOT part of the longest edge
        // (the longest edge connects TR and BL, the diagonal).
        let tl, a, b;
        if (d12 >= d01 && d12 >= d02) {
            tl = points[0];
            a = points[1];
            b = points[2];
        } else if (d02 >= d01 && d02 >= d12) {
            tl = points[1];
            a = points[0];
            b = points[2];
        } else {
            tl = points[2];
            a = points[0];
            b = points[1];
        }

        // Cross product (a-tl) x (b-tl); in image coords (y down), if
        // positive then a->b is a clockwise turn, meaning a is TR and
        // b is BL (TL->TR->BL is a clockwise sweep for this encoder's
        // finder layout).
        const cross = (a.x - tl.x) * (b.y - tl.y) - (a.y - tl.y) * (b.x - tl.x);
        if (cross > 0) {
            return { tl, tr: a, bl: b };
        }
        return { tl, tr: b, bl: a };
    }

    // Samples a single module's binarized value using the affine
    // transform derived from the 3 finder centers (module-space ->
    // pixel-space), averaging a small neighborhood to reduce noise.
    function sampleModule(bin, width, height, tlPx, trPx, blPx, mx, my, radius) {
        const s = (mx - TL_MOD.x) / (TR_MOD.x - TL_MOD.x);
        const t = (my - TL_MOD.y) / (BL_MOD.y - TL_MOD.y);
        const px = tlPx.x + s * (trPx.x - tlPx.x) + t * (blPx.x - tlPx.x);
        const py = tlPx.y + s * (trPx.y - tlPx.y) + t * (blPx.y - tlPx.y);

        let dark = 0;
        let total = 0;
        for (let dy = -radius; dy <= radius; dy++) {
            for (let dx = -radius; dx <= radius; dx++) {
                const xi = Math.round(px) + dx;
                const yi = Math.round(py) + dy;
                if (xi < 0 || xi >= width || yi < 0 || yi >= height) continue;
                dark += bin[yi * width + xi];
                total++;
            }
        }
        if (total === 0) return 0;
        return dark / total >= 0.5 ? 1 : 0;
    }

    function sampleMatrix(bin, width, height, tlPx, trPx, blPx, moduleSize) {
        // A fixed averaging window is proportionally too large once a
        // module is only a few pixels wide (blurs across neighboring
        // modules) - scale it down for small captures, same idea as
        // for a large one where a wider window better resists noise.
        const radius = moduleSize >= 6 ? 1 : 0;
        const matrix = [];
        for (let r = 0; r < SIZE; r++) {
            const row = new Uint8Array(SIZE);
            for (let c = 0; c < SIZE; c++) {
                row[c] = sampleModule(bin, width, height, tlPx, trPx, blPx, c, r, radius);
            }
            matrix.push(row);
        }
        return matrix;
    }

    // --- function-module map (must match qrcode.js exactly) ---

    function isFinderArea(r, c) {
        const inTopLeft = r < 9 && c < 9;
        const inTopRight = r < 9 && c >= SIZE - 8;
        const inBottomLeft = r >= SIZE - 8 && c < 9;
        return inTopLeft || inTopRight || inBottomLeft;
    }
    // Must match qrcode.js's ALIGNMENT_CENTER exactly - a version 5 QR
    // code has exactly one alignment pattern, at (30, 30) (see that
    // file's comment for why the other 3 combinations of [6, 30] each
    // collide with a finder pattern).
    const ALIGNMENT_CENTER = { r: 30, c: 30 };
    function isAlignmentArea(r, c) {
        return Math.abs(r - ALIGNMENT_CENTER.r) <= 2 && Math.abs(c - ALIGNMENT_CENTER.c) <= 2;
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

    // Format info at (8, 0..5),(8,7),(8,8),(7,8) - expected constant
    // for this encoder's fixed EC-level-L/mask-0 combination. Rather
    // than a full BCH(15,5) decode, we just check Hamming distance
    // against the one value this encoder ever writes (BCH(15,5) can
    // correct up to 3 bit errors, so distance<=3 is a solid signal).
    function formatBitsExpected() {
        const errorLevelBits = 0b01;
        const data = (errorLevelBits << 3) | MASK;
        let d = data << 10;
        const gen = 0b10100110111;
        for (let i = 4; i >= 0; i--) {
            if (d & (1 << (i + 10))) d ^= gen << i;
        }
        const bits = (data << 10) | d;
        return bits ^ 0x5412;
    }

    function readFormatBits(matrix) {
        const bits = new Array(15);
        for (let i = 0; i <= 5; i++) bits[i] = matrix[8][i];
        bits[6] = matrix[8][7];
        bits[7] = matrix[8][8];
        bits[8] = matrix[7][8];
        for (let i = 9; i <= 14; i++) bits[i] = matrix[14 - i][8];
        let val = 0;
        for (let i = 0; i < 15; i++) val = (val << 1) | bits[i];
        return val;
    }

    function hammingDistance(a, b) {
        let x = a ^ b;
        let count = 0;
        while (x) {
            count += x & 1;
            x >>= 1;
        }
        return count;
    }

    // Inverts qrcode.js's buildMatrix zigzag placement + mask to
    // recover the original codeword bytes.
    function extractCodewords(matrix) {
        const bits = [];
        let upward = true;
        for (const col of QR.qrColumnPairs()) {
            for (let i = 0; i < SIZE; i++) {
                const row = upward ? SIZE - 1 - i : i;
                for (const c of [col, col - 1]) {
                    if (isFunctionModule(row, c)) continue;
                    let bit = matrix[row][c];
                    const maskApply = (row + c) % 2 === 0;
                    if (maskApply) bit ^= 1;
                    bits.push(bit);
                }
            }
            upward = !upward;
        }

        const codewords = [];
        for (let i = 0; i + 8 <= bits.length; i += 8) {
            let byte = 0;
            for (let j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j];
            codewords.push(byte);
        }
        return codewords;
    }

    function codewordsToText(dataCodewords) {
        const bits = [];
        for (const byte of dataCodewords) {
            for (let i = 7; i >= 0; i--) bits.push((byte >> i) & 1);
        }
        let pos = 0;
        const readBits = (n) => {
            let v = 0;
            for (let i = 0; i < n; i++) v = (v << 1) | (bits[pos++] || 0);
            return v;
        };
        const mode = readBits(4);
        if (mode !== 0b0100) return null; // byte mode only, matching the encoder
        const len = readBits(8);
        if (len < 0 || len > DATA_CODEWORDS - 3) return null;
        const bytes = new Uint8Array(len);
        for (let i = 0; i < len; i++) bytes[i] = readBits(8);
        try {
            return new TextDecoder().decode(bytes);
        } catch (e) {
            return null;
        }
    }

    // Main entry point: decodes a QR code from an ImageData, or
    // returns null if none could be confidently read.
    function decode(imageData) {
        const { width, height } = imageData;
        const gray = grayscale(imageData);
        const threshold = otsuThreshold(gray);
        const bin = binarize(gray, threshold);

        const finders = findFinderPatterns(bin, width, height);
        if (!finders) return null;

        const { tl, tr, bl } = orientFinders(finders);
        const avgModuleSize = (tl.moduleSize + tr.moduleSize + bl.moduleSize) / 3;
        const matrix = sampleMatrix(bin, width, height, tl, tr, bl, avgModuleSize);

        const formatVal = readFormatBits(matrix);
        if (hammingDistance(formatVal, formatBitsExpected()) > 3) return null;

        const codewords = extractCodewords(matrix);
        if (codewords.length < DATA_CODEWORDS + EC_CODEWORDS) return null;

        const dataCodewords = codewords.slice(0, DATA_CODEWORDS);
        const scannedEC = codewords.slice(DATA_CODEWORDS, DATA_CODEWORDS + EC_CODEWORDS);
        const expectedEC = QR.computeECCodewords(dataCodewords);
        for (let i = 0; i < EC_CODEWORDS; i++) {
            if (expectedEC[i] !== scannedEC[i]) return null; // couldn't read cleanly - caller should ask for another try
        }

        return codewordsToText(dataCodewords);
    }

    return { decode };
})();
