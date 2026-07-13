// ai-assistant.js - a small floating chat widget, included on the
// main authenticated pages (see each page's closing <script> tags).
// Talks to POST /ai/chat (api/ai.go) - a signed, node-local,
// read-only assistant. Deliberately self-contained (injects its own
// markup/styles) so any page can add it with one <script> tag and
// nothing else.

(function () {
    const wallet = getWallet();
    if (!wallet || !wallet.address) return; // nothing to assist before login

    const history = []; // {role, content} - in-memory only, resets per page load

    const style = document.createElement("style");
    style.textContent = `
        #ccAiToggle {
            position: fixed; bottom: 22px; right: 22px; z-index: 9998;
            width: 54px; height: 54px; border-radius: 50%;
            background: linear-gradient(135deg, #8ef, #6bd6ff);
            border: none; cursor: pointer; font-size: 22px;
            box-shadow: 0 4px 16px rgba(0,0,0,0.4);
        }
        #ccAiPanel {
            position: fixed; bottom: 88px; right: 22px; z-index: 9999;
            width: 340px; max-width: calc(100vw - 32px);
            height: 460px; max-height: calc(100vh - 140px);
            background: #14181f; border: 1px solid rgba(255,255,255,0.12);
            border-radius: 14px; display: none; flex-direction: column;
            overflow: hidden; box-shadow: 0 8px 30px rgba(0,0,0,0.5);
            font-family: inherit;
        }
        #ccAiPanel.open { display: flex; }
        #ccAiHeader {
            padding: 12px 14px; background: rgba(255,255,255,0.04);
            border-bottom: 1px solid rgba(255,255,255,0.08);
            display: flex; justify-content: space-between; align-items: center;
        }
        #ccAiHeader strong { color: #8ef; font-size: 14px; }
        #ccAiClose { background: none; border: none; color: #aaa; cursor: pointer; font-size: 16px; }
        #ccAiMessages {
            flex: 1; overflow-y: auto; padding: 12px; font-size: 13px; line-height: 1.5;
        }
        .cc-ai-msg { margin-bottom: 10px; white-space: pre-wrap; }
        .cc-ai-msg.user { color: #cde; text-align: right; }
        .cc-ai-msg.assistant { color: #e6e6e6; }
        .cc-ai-msg.system { color: #999; font-style: italic; font-size: 12px; }
        #ccAiInputRow { display: flex; border-top: 1px solid rgba(255,255,255,0.08); }
        #ccAiInput {
            flex: 1; background: transparent; border: none; color: #fff;
            padding: 10px; font-size: 13px; resize: none;
        }
        #ccAiInput:focus { outline: none; }
        #ccAiSend {
            background: #8ef; color: #14181f; border: none; padding: 0 16px;
            font-weight: 600; cursor: pointer;
        }
        #ccAiSend:disabled { opacity: 0.5; cursor: default; }
    `;
    document.head.appendChild(style);

    const toggle = document.createElement("button");
    toggle.id = "ccAiToggle";
    toggle.title = "CreditChain Assistant";
    toggle.innerText = "💬";

    const panel = document.createElement("div");
    panel.id = "ccAiPanel";
    panel.innerHTML = `
        <div id="ccAiHeader">
            <strong>CreditChain Assistant</strong>
            <button id="ccAiClose">✕</button>
        </div>
        <div id="ccAiMessages">
            <div class="cc-ai-msg system">Ask me about loan affordability, Trading Hub rules, reputation, or how CreditChain works. I can look things up and calculate numbers, but I never take actions for you - you'll always click the real button yourself.</div>
        </div>
        <div id="ccAiInputRow">
            <textarea id="ccAiInput" rows="1" placeholder="Ask a question..."></textarea>
            <button id="ccAiSend">Send</button>
        </div>
    `;

    document.body.appendChild(toggle);
    document.body.appendChild(panel);

    const messagesEl = panel.querySelector("#ccAiMessages");
    const inputEl = panel.querySelector("#ccAiInput");
    const sendEl = panel.querySelector("#ccAiSend");

    toggle.addEventListener("click", () => {
        panel.classList.toggle("open");
        if (panel.classList.contains("open")) inputEl.focus();
    });
    panel.querySelector("#ccAiClose").addEventListener("click", () => panel.classList.remove("open"));

    function appendMessage(role, text) {
        const div = document.createElement("div");
        div.className = "cc-ai-msg " + role;
        div.innerText = text;
        messagesEl.appendChild(div);
        messagesEl.scrollTop = messagesEl.scrollHeight;
        return div;
    }

    async function sendMessage() {
        const text = inputEl.value.trim();
        if (!text) return;

        inputEl.value = "";
        sendEl.disabled = true;
        appendMessage("user", text);
        history.push({ role: "user", content: text });

        const thinkingEl = appendMessage("system", "Thinking...");

        try {
            const result = await apiPostSigned("/ai/chat", { messages: history });
            thinkingEl.remove();
            appendMessage("assistant", result.reply);
            history.push({ role: "assistant", content: result.reply });
        } catch (e) {
            thinkingEl.remove();
            appendMessage("system", "The assistant isn't available right now: " + e.message);
        } finally {
            sendEl.disabled = false;
        }
    }

    sendEl.addEventListener("click", sendMessage);
    inputEl.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            sendMessage();
        }
    });
})();
