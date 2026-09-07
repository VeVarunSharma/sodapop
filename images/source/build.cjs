// Optional artwork exporter. The finished SVGs are standalone, outlined masters.
// Usage: node images/source/build.cjs "/path/Avenir Next.ttc" "/path/Menlo.ttc"
const fs = require("node:fs");
const path = require("node:path");
const { Resvg } = require("@resvg/resvg-js");

const fontFiles = process.argv.slice(2);
if (fontFiles.length !== 2) {
  throw new Error("Supply the Avenir Next and Menlo font files. No fonts are redistributed.");
}
for (const file of fontFiles) fs.accessSync(file, fs.constants.R_OK);

const output = path.resolve(__dirname, "..");
const fonts = { fontFiles, loadSystemFonts: false, defaultFontFamily: "Avenir Next" };
const palette = {
  ink: "#09090B",
  panel: "#111116",
  raised: "#1A1A22",
  border: "#30303D",
  white: "#F4F4F5",
  muted: "#9292A3",
  pink: "#FB7185",
  purple: "#C084FC",
  cyan: "#67E8F9",
};
const escape = (value) => String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll('"', "&quot;");

const defs = `
  <linearGradient id="brand" x1="0" y1="0" x2="1" y2=".2">
    <stop stop-color="${palette.pink}"/><stop offset="1" stop-color="${palette.purple}"/>
  </linearGradient>
  <linearGradient id="can-body" x1="0" y1=".15" x2="1" y2=".75">
    <stop stop-color="#FDA4AF"/><stop offset=".25" stop-color="${palette.pink}"/>
    <stop offset=".66" stop-color="${palette.purple}"/><stop offset="1" stop-color="#8350BE"/>
  </linearGradient>
  <linearGradient id="can-edge">
    <stop stop-color="${palette.cyan}" stop-opacity=".85"/>
    <stop offset=".52" stop-color="${palette.cyan}" stop-opacity=".08"/>
    <stop offset="1" stop-color="${palette.cyan}" stop-opacity="0"/>
  </linearGradient>
  <linearGradient id="silver" x1=".12" y1="0" x2=".85" y2="1">
    <stop stop-color="#FFFFFF"/><stop offset=".3" stop-color="#DCE4ED"/>
    <stop offset=".58" stop-color="#9292A3"/><stop offset=".81" stop-color="#E8EDF3"/>
    <stop offset="1" stop-color="#A5A7BA"/>
  </linearGradient>
  <linearGradient id="rim" x1="0" y1="0" x2="0" y2="1">
    <stop stop-color="#F4F4F5"/><stop offset=".5" stop-color="#BCC3D0"/>
    <stop offset="1" stop-color="#70748A"/>
  </linearGradient>
  <linearGradient id="tab" x1="0" y1="0" x2="1" y2="1">
    <stop stop-color="#FFFFFF"/><stop offset=".45" stop-color="#DCE4ED"/>
    <stop offset="1" stop-color="#9B9EB3"/>
  </linearGradient>
  <radialGradient id="halo">
    <stop stop-color="#C084FC" stop-opacity=".16"/><stop offset=".56" stop-color="#FB7185" stop-opacity=".055"/>
    <stop offset="1" stop-color="#09090B" stop-opacity="0"/>
  </radialGradient>
  <radialGradient id="cyan-halo">
    <stop stop-color="#67E8F9" stop-opacity=".08"/><stop offset="1" stop-color="#09090B" stop-opacity="0"/>
  </radialGradient>
  <radialGradient id="icon-bg">
    <stop stop-color="#23202F"/><stop offset="1" stop-color="#09090B"/>
  </radialGradient>
  <radialGradient id="ground">
    <stop stop-color="#09090B" stop-opacity=".24"/><stop offset="1" stop-color="#09090B" stop-opacity="0"/>
  </radialGradient>`;

function text(value, x, y, size, color = palette.white, extra = "") {
  return `<text x="${x}" y="${y}" font-family="Avenir Next" font-weight="500" font-size="${size}" fill="${color}" ${extra}>${escape(value)}</text>`;
}

function mono(value, x, y, size = 16, color = palette.muted, extra = "") {
  return `<text x="${x}" y="${y}" font-family="Menlo" font-size="${size}" fill="${color}" ${extra}>${escape(value)}</text>`;
}

function document(width, height, body) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">
    <defs>${defs}</defs>${body}</svg>`;
}

const word = text("sodapop", 0, 300, 200, palette.white, 'font-weight="900" letter-spacing="-7"')
  .replace('font-weight="500" ', "");
const wordRenderer = new Resvg(document(1600, 800, word), { font: fonts });
const wordBounds = wordRenderer.getBBox();
if (!wordBounds || wordBounds.width < 400) throw new Error("The wordmark font did not load.");
// Outline before applying the gradient so SVG export never relies on text paint servers.
const wordSvg = wordRenderer.toString();
const outlinedWord = wordSvg.slice(wordSvg.indexOf(">") + 1, wordSvg.lastIndexOf("</svg>"))
  .replace(/<defs>[\s\S]*?<\/defs>/, "");
if (!outlinedWord.includes('fill="#f4f4f5"')) throw new Error("The outlined wordmark is missing its fill.");

function wordmark(x, y, width, color = "url(#brand)") {
  const scale = width / wordBounds.width;
  return `<g transform="translate(${x} ${y}) scale(${scale}) translate(${-wordBounds.x} ${-wordBounds.y})">
    ${outlinedWord.replaceAll('fill="#f4f4f5"', `fill="${color}"`)}</g>`;
}

function slashes(x, y, count = 5, size = 30, opacity = 1) {
  let strokes = "";
  for (let i = 0; i < count; i++) {
    const left = i * size * .58;
    strokes += `<path d="M${left} ${size}l${size * .48} ${-size}"/>`;
  }
  return `<g transform="translate(${x} ${y})" opacity="${opacity}" fill="none" stroke="url(#brand)" stroke-width="${size * .16}" stroke-linecap="round">${strokes}</g>`;
}

// The same raised pull tab, pink/purple can, cyan limbs, and wink as the TUI mascot.
function mascot() {
  return `
    <ellipse cx="324" cy="685" rx="206" ry="30" fill="url(#ground)"/>
    <g stroke="#262034" stroke-width="18" stroke-linecap="round" stroke-linejoin="round" fill="none">
      <path d="M175 419C120 416 109 438 104 470"/>
      <path d="M466 419C524 427 541 393 540 350"/>
    </g>
    <g stroke="${palette.cyan}" stroke-width="10" stroke-linecap="round" stroke-linejoin="round" fill="none">
      <path d="M175 419C120 416 109 438 104 470"/>
      <path d="M466 419C524 427 541 393 540 350"/>
    </g>
    <path d="M91 460C79 465 80 480 88 487C101 499 121 490 119 477L116 466"
      fill="${palette.cyan}" stroke="#262034" stroke-width="6" stroke-linejoin="round"/>
    <path d="M523 357C512 351 506 339 508 329C510 320 518 322 523 332L524 304
      C524 294 536 294 537 304L539 325L545 294C547 284 559 287 557 297L552 327
      L566 307C572 299 581 307 575 315L559 343C550 356 540 361 523 357Z"
      fill="${palette.cyan}" stroke="#262034" stroke-width="6" stroke-linejoin="round"/>
    <path d="M544 338q-8-8-15-4" fill="none" stroke="#FFFFFF" stroke-opacity=".48" stroke-width="4" stroke-linecap="round"/>
    <g stroke="#262034" stroke-width="7" stroke-linejoin="round">
      <path d="M235 619L226 651C219 653 198 654 199 666C200 682 257 680 264 669L271 628"
        fill="${palette.cyan}"/>
      <path d="M367 623L375 651C389 648 415 652 418 665C420 680 365 682 357 669L350 632"
        fill="${palette.cyan}"/>
    </g>
    <path d="M204 667q22 6 53 0M365 670q24 4 46-3" fill="none" stroke="#2C7487" stroke-width="5" stroke-linecap="round"/>
    <path d="M172 213C181 187 455 187 464 213L478 564
      C480 611 451 640 325 644C198 643 169 619 168 577Z"
      fill="url(#can-body)" stroke="#30233E" stroke-width="7" stroke-linejoin="round"/>
    <path d="M173 218C187 208 213 209 237 217L229 605Q202 621 183 598Z"
      fill="url(#can-edge)"/>
    <path d="M419 217Q449 210 463 224L474 565Q474 604 443 616L422 617Z"
      fill="#552775" opacity=".18"/>
    <path d="M193 296L190 500" fill="none" stroke="#FFFFFF" stroke-opacity=".52" stroke-width="7" stroke-linecap="round"/>
    <path d="M193 263v11" fill="none" stroke="#FFFFFF" stroke-opacity=".68" stroke-width="7" stroke-linecap="round"/>
    <path d="M177 585C215 620 431 631 473 582L464 607C433 646 216 649 184 607Z"
      fill="url(#rim)" stroke="#30233E" stroke-width="5"/>
    <path d="M193 602C246 628 417 626 456 604" fill="none" stroke="#FFFFFF" stroke-opacity=".6" stroke-width="3"/>
    <path d="M169 209Q175 250 321 256Q455 255 468 215L467 199L171 197Z"
      fill="url(#rim)" stroke="#30233E" stroke-width="5"/>
    <ellipse cx="319" cy="201" rx="150" ry="44" fill="url(#silver)" stroke="#30233E" stroke-width="6"/>
    <ellipse cx="319" cy="200" rx="132" ry="32" fill="none" stroke="#F4F4F5" stroke-opacity=".8" stroke-width="3"/>
    <path d="M197 186Q239 163 313 169" fill="none" stroke="#FFFFFF" stroke-width="5" stroke-linecap="round"/>
    <ellipse cx="304" cy="203" rx="30" ry="14" fill="#343242"/>
    <ellipse cx="303" cy="200" rx="24" ry="8" fill="#171622"/>
    <g transform="rotate(30 360 164)">
      <path d="M337 193L335 143C333 105 389 103 391 140L389 188
        Q385 207 360 207Q341 205 337 193Z" fill="url(#tab)" stroke="#474355" stroke-width="5"/>
      <path d="M347 147C346 125 378 125 379 147L378 160Q363 169 348 160Z"
        fill="#242131" stroke="#8F8FA2" stroke-width="3"/>
      <ellipse cx="362" cy="185" rx="9" ry="6" fill="#9A9DB0"/>
      <path d="M342 145Q341 119 365 118" fill="none" stroke="#FFFFFF" stroke-width="4" stroke-linecap="round"/>
    </g>
    <g fill="${palette.white}" stroke="#FFFFFF" stroke-width="2">
      <path d="M276 198Q271 188 282 184Q284 171 296 178Q307 167 315 181
        Q327 179 330 192Q328 203 314 205Q290 208 276 198Z"/>
    </g>
    <circle cx="288" cy="144" r="10" fill="${palette.cyan}" fill-opacity=".22" stroke="${palette.cyan}" stroke-width="4"/>
    <circle cx="262" cy="96" r="16" fill="#FB7185" fill-opacity=".1" stroke="#FB7185" stroke-width="4"/>
    <path d="M253 91q4-6 10-5" fill="none" stroke="#FFFFFF" stroke-opacity=".72" stroke-width="3" stroke-linecap="round"/>
    <circle cx="301" cy="56" r="7" fill="${palette.purple}"/>
    <path d="M245 361q22-24 43 0" fill="none" stroke="#292035" stroke-width="12" stroke-linecap="round"/>
    <ellipse cx="367" cy="361" rx="15" ry="21" fill="#292035"/>
    <ellipse cx="371" cy="355" rx="4" ry="6" fill="#FFFFFF" fill-opacity=".88"/>
    <ellipse cx="234" cy="400" rx="16" ry="9" fill="#FFFFFF" fill-opacity=".23"/>
    <ellipse cx="389" cy="400" rx="16" ry="9" fill="#FFFFFF" fill-opacity=".16"/>
    <path d="M281 408C294 444 342 449 360 409" fill="none" stroke="#292035" stroke-width="12" stroke-linecap="round"/>
    <path d="M264 324q10-7 20-4M354 322q13-2 23 7" fill="none" stroke="#663C75" stroke-width="5" stroke-linecap="round"/>
    <rect x="215" y="478" width="214" height="60" rx="15" fill="#F4F4F5" fill-opacity=".94"/>
    ${text("SODAPOP", 322, 517, 25, "#35213E", 'font-weight="800" letter-spacing="2" text-anchor="middle"').replace('font-weight="500" ', "")}
    <path d="M253 558l-10 15m38-15-10 15m38-15-10 15" fill="none" stroke="${palette.cyan}" stroke-width="6" stroke-linecap="round"/>
    <circle cx="392" cy="562" r="7" fill="none" stroke="#F4F4F5" stroke-opacity=".4" stroke-width="2"/>
    <circle cx="416" cy="550" r="4" fill="#F4F4F5" fill-opacity=".35"/>
  `;
}

function mark() {
  return `<g transform="rotate(-7 256 264)">
    <path d="M148 159Q153 131 254 130Q355 131 364 158L368 365
      Q368 410 258 414Q148 411 145 373Z" fill="url(#can-body)" stroke="#392648" stroke-width="5"/>
    <path d="M151 168Q165 157 190 166L186 375Q169 383 155 369Z" fill="url(#can-edge)"/>
    <path d="M164 193v85" fill="none" stroke="#FFFFFF" stroke-opacity=".5" stroke-width="5" stroke-linecap="round"/>
    <path d="M151 375Q251 416 364 373L357 394Q255 432 158 395Z" fill="url(#rim)" stroke="#392648" stroke-width="4"/>
    <path d="M146 148Q150 182 255 185Q361 183 366 151V144H146Z" fill="url(#rim)" stroke="#392648" stroke-width="4"/>
    <ellipse cx="256" cy="147" rx="109" ry="32" fill="url(#silver)" stroke="#392648" stroke-width="5"/>
    <ellipse cx="256" cy="147" rx="93" ry="23" fill="none" stroke="#FFFFFF" stroke-width="2" stroke-opacity=".65"/>
    <ellipse cx="245" cy="149" rx="23" ry="10" fill="#292035"/>
    <g transform="rotate(28 288 126)">
      <rect x="272" y="82" width="38" height="73" rx="17" fill="url(#tab)" stroke="#474355" stroke-width="4"/>
      <rect x="282" y="94" width="18" height="25" rx="9" fill="#262034"/>
      <ellipse cx="291" cy="139" rx="6" ry="4" fill="#9292A3"/>
    </g>
    <path d="M212 253q15-17 31 0" fill="none" stroke="#292035" stroke-width="10" stroke-linecap="round"/>
    <ellipse cx="297" cy="254" rx="12" ry="16" fill="#292035"/>
    <ellipse cx="300" cy="249" rx="3" ry="4" fill="#FFFFFF"/>
    <path d="M235 294q24 34 48 0" fill="none" stroke="#292035" stroke-width="10" stroke-linecap="round"/>
    <ellipse cx="203" cy="282" rx="11" ry="6" fill="#FFFFFF" fill-opacity=".22"/>
    <ellipse cx="316" cy="283" rx="11" ry="6" fill="#FFFFFF" fill-opacity=".18"/>
    <path d="M237 345l-7 11m26-11-7 11m26-11-7 11" fill="none" stroke="#F4F4F5" stroke-opacity=".88" stroke-width="5" stroke-linecap="round"/>
    <circle cx="214" cy="104" r="9" fill="none" stroke="${palette.cyan}" stroke-width="4"/>
    <circle cx="235" cy="66" r="7" fill="${palette.pink}"/>
  </g>`;
}

function favicon() {
  return `<rect width="64" height="64" rx="14" fill="${palette.ink}"/>
    <rect x="17" y="22" width="31" height="33" rx="8" fill="url(#brand)"/>
    <ellipse cx="32.5" cy="23" rx="15.5" ry="4.5" fill="#DCE4ED"/>
    <rect x="35" y="12" width="7" height="14" rx="3.5" fill="#DCE4ED" transform="rotate(20 38 21)"/>
    <path d="M24 36q3-4 6 0" fill="none" stroke="#292035" stroke-width="2.8" stroke-linecap="round"/>
    <circle cx="39" cy="35.5" r="2.4" fill="#292035"/>
    <path d="M29 43q4 5 8 0" fill="none" stroke="#292035" stroke-width="2.8" stroke-linecap="round"/>
    <circle cx="25" cy="13" r="3" fill="${palette.cyan}"/>`;
}

function background(width, height, cx, cy, radius) {
  return `<rect width="${width}" height="${height}" fill="${palette.ink}"/>
    <ellipse cx="${cx}" cy="${cy}" rx="${radius * 1.6}" ry="${radius * 1.45}" fill="url(#halo)"/>
    <ellipse cx="${width * .12}" cy="${height}" rx="${width * .4}" ry="${height * .7}" fill="url(#cyan-halo)"/>
    <circle cx="${cx}" cy="${cy}" r="${radius}" fill="none" stroke="#C084FC" stroke-opacity=".12" stroke-width="1.5"/>
    <circle cx="${cx}" cy="${cy}" r="${radius * .85}" fill="none" stroke="#C084FC" stroke-opacity=".05" stroke-width="1.5"/>`;
}

function social(width, height) {
  const ratio = width / 1280;
  return background(width, height, width * .815, height * .51, height * .36) +
    `<g transform="scale(${ratio})">` +
    `<rect x="74" y="82" width="294" height="36" rx="18" fill="${palette.raised}" stroke="${palette.border}"/>` +
    `<path d="m91 94 5 5-5 5m12 0h7" fill="none" stroke="${palette.cyan}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>` +
    mono("TERMINAL CODING COMPANION", 122, 105, 13, "#BFC0CE") +
    wordmark(70, 188, 689) +
    text("Your terminal, with a little more pop.", 76, 406, 27, "#D5D5DF") +
    slashes(78, 458, 5, 22, .9) +
    mono("sodapop.sh", 78, 551, 18, palette.white) +
    `<rect x="74" y="590" width="1132" height="1" fill="url(#brand)" opacity=".32"/>` +
    `<g transform="translate(810 44) scale(.735)">${mascot()}</g></g>`;
}

const assets = [
  {
    name: "sodapop-logo", width: 1024, height: 1024, transparent: true,
    title: "Sodapop logo", use: "Transparent primary can mark for avatars, stickers, and general branding.",
    body: `<g transform="scale(2)">${mark()}</g>`,
  },
  {
    name: "sodapop-oauth", width: 512, height: 512, transparent: false,
    title: "Sodapop OAuth application icon", use: "Upload this PNG as the GitHub OAuth app logo. Set badge background to #09090B.",
    body: `<rect width="512" height="512" fill="${palette.ink}"/><circle cx="256" cy="256" r="256" fill="url(#icon-bg)"/>${mark()}`,
  },
  {
    name: "sodapop-mascot", width: 1200, height: 1200, transparent: true,
    title: "Sodapop mascot", use: "Transparent full character for the landing-page hero, documentation, and stickers.",
    body: `<g transform="translate(75 15) scale(1.64)">${mascot()}</g>`,
  },
  {
    name: "sodapop-main", width: 1600, height: 1600, transparent: false,
    title: "Sodapop main brand image", use: "Square main image for announcements, media, and brand introductions.",
    body: background(1600, 1600, 800, 716, 500) +
      mono("MEET YOUR TERMINAL COMPANION", 94, 88, 20, "#BFC0CE") +
      slashes(1365, 55, 5, 30, .8) +
      `<g transform="translate(280 68) scale(1.62)">${mascot()}</g>` +
      wordmark(310, 1214, 980) +
      text("Your terminal, with a little more pop.", 800, 1490, 34, "#D5D5DF", 'text-anchor="middle"') +
      mono("sodapop.sh", 800, 1558, 20, palette.muted, 'text-anchor="middle"'),
  },
  {
    name: "sodapop-banner", width: 2400, height: 800, transparent: false,
    title: "Sodapop wide banner", use: "Wide repository README header, website masthead, or presentation banner.",
    body: background(2400, 800, 1950, 404, 329) +
      mono("THE TERMINAL CODING COMPANION", 146, 163, 22, "#BFC0CE") +
      wordmark(140, 243, 1170) +
      text("Your terminal, with a little more pop.", 146, 601, 39, "#D5D5DF") +
      mono("sodapop.sh", 146, 726, 23, palette.white) +
      slashes(1163, 693, 5, 26, .8) +
      `<g transform="translate(1604 26) scale(1.05)">${mascot()}</g>`,
  },
  {
    name: "sodapop-repo-social", width: 1280, height: 640, transparent: false,
    title: "Sodapop repository social preview", use: "Upload this PNG in GitHub repository Settings > Social preview.",
    body: social(1280, 640),
  },
  {
    name: "sodapop-og", width: 1200, height: 630, transparent: false,
    title: "Sodapop landing-page social card", use: "Landing-page Open Graph and social-sharing card.",
    body: social(1200, 630),
  },
  ...[
    ["on-dark", palette.white, "dark"],
    ["on-light", "#18181B", "light"],
  ].map(([suffix, color, surface]) => ({
    name: `sodapop-lockup-${suffix}`, width: 1280, height: 320, transparent: true,
    title: `Sodapop horizontal logo for ${surface} surfaces`,
    use: `Transparent can-and-wordmark lockup. Use on ${surface} backgrounds.`,
    body: `<g transform="translate(1 18) scale(.6)">${mark()}</g>` + wordmark(324, 56, 897, color),
  })),
  {
    name: "sodapop-wordmark", width: 1200, height: 340, transparent: true,
    title: "Sodapop gradient wordmark", use: "Standalone gradient lettering for headings and brand layouts.",
    body: wordmark(50, 55, 1100),
  },
  {
    name: "sodapop-favicon", width: 64, height: 64, transparent: true,
    title: "Sodapop favicon", use: "Simplified small-size mark for browser tabs. Use the SVG or the 16/32-pixel PNGs.",
    body: favicon(),
  },
];

fs.mkdirSync(output, { recursive: true });
const manifest = {
  brand: "Sodapop",
  website: "https://sodapop.sh",
  created: "2026-09-06",
  palette,
  style: "Original polished vector interpretation of the existing terminal soda-can mascot.",
  typography: "Lettering outlined from locally licensed Avenir Next; utility labels outlined from Menlo. No font files are included.",
  guidance: {
    oauth: "Use sodapop-oauth.png and badge background #09090B. Keep the supplied breathing room for circular crops.",
    repository: "Use sodapop-repo-social.png for the repository social preview. Use sodapop-banner.svg or .png for a README.",
    landing: "Use the transparent mascot and the matching light/dark lockup. Use sodapop-og.png as the social-sharing card.",
    editing: "The SVGs are self-contained vector masters with outlined text: no scripts, linked images, or external fonts.",
    affiliation: "Sodapop is independent. These assets do not contain GitHub, Copilot, or Charm logos.",
  },
  references: {
    oauth: "https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-a-custom-badge-for-your-oauth-app",
    repository: "https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/customizing-your-repositorys-social-media-preview",
  },
  assets: [],
};

for (const asset of assets) {
  const renderer = new Resvg(document(asset.width, asset.height, asset.body), { font: fonts });
  let svg = renderer.toString();
  if (/<text\b|<image\b|<script\b|<foreignObject\b/.test(svg)) {
    throw new Error(`${asset.name}: export is not standalone outlined vector artwork.`);
  }
  if (/\bid=""|url\(#\)/.test(svg)) throw new Error(`${asset.name}: export has an unnamed paint server.`);
  svg = svg.replace(/\bid="([^"]+)"/g, (_, id) => `id="${asset.name}-${id}"`)
    .replace(/url\(#([^)]*)\)/g, (_, id) => `url(#${asset.name}-${id})`)
    .replace(/\bhref="#([^"]+)"/g, (_, id) => `href="#${asset.name}-${id}"`);
  svg = svg.replace(/<svg\b/, `<svg role="img" aria-labelledby="${asset.name}-title ${asset.name}-description"`);
  svg = svg.replace(/(<svg\b[^>]*>)/, `$1\n<title id="${asset.name}-title">${escape(asset.title)}</title>\n<desc id="${asset.name}-description">${escape(asset.use)}</desc>`);
  const png = new Resvg(svg, { font: { loadSystemFonts: false } }).render().asPng();
  if (["sodapop-oauth", "sodapop-repo-social"].includes(asset.name) && png.length >= 1000000) {
    throw new Error(`${asset.name}: upload PNG must be under 1 MB, got ${png.length} bytes.`);
  }
  fs.writeFileSync(path.join(output, `${asset.name}.svg`), svg);
  fs.writeFileSync(path.join(output, `${asset.name}.png`), png);
  manifest.assets.push({
    name: asset.name, title: asset.title, use: asset.use,
    width: asset.width, height: asset.height, transparent: asset.transparent,
    svg: `${asset.name}.svg`, png: `${asset.name}.png`, pngBytes: png.length,
  });
  console.log(`${asset.name}: ${asset.width}x${asset.height}, PNG ${Math.ceil(png.length / 1024)} KiB`);
}

for (const [source, filename, size] of [
  ["sodapop-favicon.svg", "favicon-16.png", 16],
  ["sodapop-favicon.svg", "favicon-32.png", 32],
  ["sodapop-oauth.svg", "apple-touch-icon.png", 180],
]) {
  const svg = fs.readFileSync(path.join(output, source), "utf8");
  const png = new Resvg(svg, { font: { loadSystemFonts: false }, fitTo: { mode: "width", value: size } }).render().asPng();
  fs.writeFileSync(path.join(output, filename), png);
  manifest.assets.push({ name: filename, width: size, height: size, png: filename, pngBytes: png.length });
}
fs.writeFileSync(path.join(output, "brand-assets.json"), `${JSON.stringify(manifest, null, 2)}\n`);
