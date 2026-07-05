// Wallet backup phrase - encodes the raw private key as a sequence of
// words instead of a hex string, so it's easier to write down/read
// back correctly by hand. This is a SIMPLIFIED, testnet-only scheme -
// NOT the industry-standard BIP39 (which uses an 11-bit/2048-word
// checksum-carrying encoding for a 12-24 word phrase). Here, each word
// maps to exactly one byte (a 256-word list, one word per possible
// byte value 0-255), so the full 64-byte private key becomes exactly
// 64 words - longer than a real BIP39 phrase, but trivially and
// exactly reversible with no derivation risk (the alternative -
// encoding only the 32-byte seed and re-deriving the public key in JS
// - was deliberately avoided so there's no custom Ed25519 key-
// derivation code whose correctness this depends on). Replace this
// with real BIP39 (or better) before any of this holds real value.
const MNEMONIC_WORDLIST = [
    "abbey","acid","acorn","actor","admit","adobe","adult","after","agile","alarm",
    "album","alert","alien","alike","alive","allow","alloy","almond","alone","along",
    "alpha","amber","amend","among","ample","amuse","anchor","angel","anger","angle",
    "ankle","annex","apple","apron","arbor","arena","argue","arise","armor","arrow",
    "aside","asset","atlas","atom","audit","aunt","autumn","avoid","awake","award",
    "badge","baker","balmy","bamboo","banjo","barge","basil","basin","batch","beach",
    "beacon","beard","beast","begin","belly","bench","berry","bike","birch","bison",
    "blade","blaze","blend","bliss","block","bloom","blush","boast","bonus","booth",
    "bored","born","brave","bread","breed","brick","bride","brief","bright","broom",
    "brown","brush","buddy","bugle","bunch","burst","cabin","cable","cadet","camel",
    "canal","candy","canoe","canyon","cargo","carve","cedar","cello","chalk","charm",
    "chase","cheek","chess","chief","chill","choir","chord","chose","churn","cider",
    "civic","claim","clamp","clash","clasp","cliff","climb","cling","cloak","clock",
    "cloth","cloud","clove","clown","coach","coast","cobra","cocoa","comet","comic",
    "coral","couch","cough","cover","crane","crank","crash","crawl","cream","creek",
    "crest","crime","crisp","cross","crowd","crown","cruel","crumb","crush","curly",
    "curve","cycle","daisy","dance","dandy","dawn","decoy","delta","depth","desert",
    "diner","dizzy","donor","donut","dough","dozen","draft","drain","drake","dream",
    "dress","drift","drill","drive","drone","drown","dune","dusty","eager","eagle",
    "early","earth","easel","east","edge","eight","elbow","elder","elite","elope",
    "email","ember","empty","enemy","enjoy","enter","entry","equal","erase","error",
    "essay","event","exact","exile","extra","fable","faint","fairy","faith","false",
    "fancy","fatal","favor","feast","fence","ferry","fetch","fever","fiber","field",
    "fifty","filter","final","finch","first","flame","flare","flask","fleet","flesh",
    "flick","flint","float","flock","flood","floor","flora","flour","foamy","focus",
    "folio","force","forge","fossil","frame","fresh"
];

function mnemonicWordListIsValid() {
    return MNEMONIC_WORDLIST.length === 256;
}

// privateKeyToMnemonic converts a hex-encoded private key into a
// space-separated word phrase - one word per byte.
function privateKeyToMnemonic(hexPrivateKey) {
    const bytes = hexToBytes(hexPrivateKey);
    return Array.from(bytes).map((b) => MNEMONIC_WORDLIST[b]).join(" ");
}

// mnemonicToPrivateKey reverses privateKeyToMnemonic - throws if any
// word isn't recognized, so a typo is caught immediately rather than
// silently producing a wrong key.
function mnemonicToPrivateKey(phrase) {
    const words = phrase.trim().toLowerCase().split(/\s+/);
    const bytes = new Uint8Array(words.length);
    for (let i = 0; i < words.length; i++) {
        const idx = MNEMONIC_WORDLIST.indexOf(words[i]);
        if (idx === -1) {
            throw new Error(`"${words[i]}" is not a recognized backup-phrase word (position ${i + 1})`);
        }
        bytes[i] = idx;
    }
    return bytesToHex(bytes);
}
