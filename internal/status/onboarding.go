package status

// onboardingPage — professional first-run wizard. No emoji icons, clean
// sections. Language selector, music source, BYOK LLM (provider presets +
// URL + key + model + live Test), and voice (provider + voice). POSTs JSON
// to /onboarding which writes config.json.
const onboardingPage = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>radio-dj · setup</title>
<style>
  :root{--bg:#f4f1ec;--paper:#fff;--ink:#1a1a1a;--soft:#6b6b6b;--line:#1a1a1a;--accent:#c9795a;--ok:#3a8a3a;--err:#b03a3a}
  *{box-sizing:border-box;margin:0;padding:0}
  body{background:var(--bg);color:var(--ink);font-family:-apple-system,"Segoe UI",Helvetica,Arial,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
  .card{width:100%;max-width:560px;background:var(--paper);border:2px solid var(--line);box-shadow:6px 6px 0 var(--line)}
  .hd{padding:18px 24px;border-bottom:2px solid var(--line);display:flex;justify-content:space-between;align-items:center}
  .hd h1{font-size:18px;font-weight:800;letter-spacing:.5px}
  .hd .v{font-size:11px;color:var(--soft);font-weight:700;text-transform:uppercase;letter-spacing:1px}
  .bd{padding:24px}
  .sec{margin-bottom:22px}
  .sec h2{font-size:11px;font-weight:800;text-transform:uppercase;letter-spacing:1.5px;color:var(--soft);margin-bottom:10px;padding-bottom:6px;border-bottom:1px solid #e0ddd6}
  label{display:block;font-size:12px;font-weight:700;margin:10px 0 4px}
  input,select{width:100%;padding:10px 12px;border:2px solid var(--line);background:var(--paper);font-family:inherit;font-size:14px;font-weight:500}
  input:focus,select:focus{outline:none;border-color:var(--accent)}
  .grid2{display:grid;grid-template-columns:1fr 1fr;gap:12px}
  .row{display:flex;gap:8px;align-items:flex-end}
  .row>div{flex:1}
  .test{padding:10px 16px;border:2px solid var(--line);background:#eee;font-weight:800;font-size:13px;text-transform:uppercase;letter-spacing:.5px;cursor:pointer;white-space:nowrap}
  .test:active{transform:translate(1px,1px)}
  .res{font-size:12px;font-weight:700;margin-top:6px;min-height:16px}
  .res.ok{color:var(--ok)} .res.err{color:var(--err)}
  .save{width:100%;margin-top:8px;padding:14px;border:2px solid var(--line);background:var(--ink);color:#fff;font-weight:800;font-size:15px;text-transform:uppercase;letter-spacing:1px;cursor:pointer}
  .save:active{transform:translate(2px,2px)}
  .done{margin-top:14px;padding:14px;border:2px solid var(--ok);background:#eaf3ea;font-weight:700;display:none}
  .hint{font-size:11px;color:var(--soft);margin-top:3px}
</style>
</head>
<body>
<form class="card" id="f">
  <div class="hd">
    <h1>radio-dj · setup</h1>
    <span class="v">configuration</span>
  </div>
  <div class="bd">

    <div class="sec">
      <h2>General</h2>
      <div class="grid2">
        <div><label>Language / Idioma</label>
          <select id="language"><option value="es">Español</option><option value="en">English</option></select></div>
        <div><label>Station name</label>
          <input id="station_name" value="radio-dj"></div>
      </div>
      <label>Location (for time + weather)</label>
      <input id="location" value="La Paz">
    </div>

    <div class="sec">
      <h2>Music library</h2>
      <label>Music folder</label>
      <input id="library" placeholder="/home/you/Music">
      <div class="hint">Point at your music folder. Leave Navidrome empty to use the folder directly.</div>
      <label>Navidrome URL (optional)</label>
      <input id="navidrome_url" placeholder="http://localhost:4533">
      <div class="grid2" style="margin-top:8px">
        <div><label>Navidrome user</label><input id="navidrome_user"></div>
        <div><label>Navidrome password</label><input id="navidrome_pass" type="password"></div>
      </div>
    </div>

    <div class="sec">
      <h2>AI provider (BYOK)</h2>
      <div class="grid2">
        <div><label>Provider</label>
          <select id="llm_provider">
            <option value="glm">GLM (Z.ai)</option>
            <option value="openai">OpenAI</option>
            <option value="openrouter">OpenRouter</option>
            <option value="ollama">Ollama (local)</option>
            <option value="groq">Groq</option>
            <option value="custom">Custom</option>
          </select></div>
        <div><label>Model</label><input id="glm_model" value="glm-4.6"></div>
      </div>
      <label>API base URL</label>
      <input id="glm_base_url" value="https://api.z.ai/api/coding/paas/v4">
      <label>API key</label>
      <input id="glm_api_key" type="password" placeholder="your key">
      <div class="row" style="margin-top:8px">
        <div></div>
        <button type="button" class="test" id="t">Test connection</button>
      </div>
      <div class="res" id="tres"></div>
    </div>

    <div class="sec">
      <h2>Voice</h2>
      <div class="grid2">
        <div><label>TTS provider</label>
          <select id="voice_provider">
            <option value="edge-tts">Edge TTS (cloud)</option>
            <option value="piper">Piper (local)</option>
            <option value="say">macOS say (built-in)</option>
          </select></div>
        <div><label>Voice</label><input id="voice" value="es-CO-SalomeNeural"></div>
      </div>
      <div class="hint" id="vhint">Edge TTS voices: es-CO-SalomeNeural (Colombia), es-MX-DaliaNeural (México), es-ES-ElviraNeural (España)…</div>
    </div>

    <button type="submit" class="save">Save and start</button>
    <div class="done" id="done">Saved. Restart radio-dj (if installed via <code>./radio-dj install</code>, it restarts itself).</div>
  </div>
</form>
<script>
const LLM = {
  glm:       {url:"https://api.z.ai/api/coding/paas/v4", model:"glm-4.6"},
  openai:    {url:"https://api.openai.com/v1",          model:"gpt-4o-mini"},
  openrouter:{url:"https://openrouter.ai/api/v1",       model:"openai/gpt-4o-mini"},
  ollama:    {url:"http://localhost:11434/v1",          model:"llama3.1"},
  groq:      {url:"https://api.groq.com/openai/v1",     model:"llama-3.3-70b-versatile"},
  custom:    {url:"", model:""},
};
document.getElementById("llm_provider").addEventListener("change", e=>{
  const p = LLM[e.target.value]; if(!p) return;
  document.getElementById("glm_base_url").value = p.url;
  document.getElementById("glm_model").value = p.model;
});
document.getElementById("t").addEventListener("click", async ()=>{
  const r = document.getElementById("tres"); r.className="res"; r.textContent="testing…";
  try{
    const res = await fetch("/onboarding/test",{method:"POST",headers:{"Content-Type":"application/json"},
      body:JSON.stringify({base_url:glm_base_url.value, api_key:glm_api_key.value, model:glm_model.value})});
    const j = await res.json();
    if(j.ok){ r.className="res ok"; r.textContent="OK — connection works."; }
    else { r.className="res err"; r.textContent="Failed: "+(j.error||"unknown"); }
  }catch(e){ r.className="res err"; r.textContent="Failed: "+e; }
});
document.getElementById("f").addEventListener("submit", async e=>{
  e.preventDefault();
  const v = id=>document.getElementById(id).value.trim();
  const cfg = {
    language: v("language"),
    station_name: v("station_name")||"radio-dj",
    location: v("location"),
    library: v("library"),
    source: v("navidrome_url")?"navidrome":"folder",
    navidrome_url: v("navidrome_url"),
    navidrome_user: v("navidrome_user"),
    navidrome_pass: v("navidrome_pass"),
    llm_provider: v("llm_provider"),
    glm_base_url: v("glm_base_url"),
    glm_api_key: v("glm_api_key"),
    glm_model: v("glm_model"),
    voice_provider: v("voice_provider"),
    voice: v("voice"),
  };
  const res = await fetch("/onboarding",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(cfg)});
  if(res.ok) document.getElementById("done").style.display="block";
});
</script>
</body>
</html>`
