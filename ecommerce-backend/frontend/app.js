/* ShopSphere: a dependency-free single-page UI for the e-commerce backend.
 * The gateway serves this file and the API from the same origin, so no CORS setup is needed. */

const state = {
  token: localStorage.getItem("shopsphere.token"),
  user: JSON.parse(localStorage.getItem("shopsphere.user") || "null"),
  filters: { category: "", q: "" },
};

const money = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR", maximumFractionDigits: 2 });
const $ = (selector, root = document) => root.querySelector(selector);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const cap = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

function fmtDate(value) {
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" });
}

class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

/* ---------------------------------------------------------------- API client */

async function api(path, { method = "GET", body, auth = false, raw = false } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth) {
    if (!state.token) throw new ApiError(401, "Please sign in first.");
    headers.Authorization = `Bearer ${state.token}`;
  }

  let res;
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, "Cannot reach the server. Is the gateway running?");
  }

  if (res.status === 401 && auth) {
    signOut(true);
    throw new ApiError(401, "Your session has expired. Please sign in again.");
  }
  if (raw) return res;

  const isJson = res.headers.get("content-type")?.includes("application/json");
  const data = isJson ? await res.json() : null;
  if (!res.ok) throw new ApiError(res.status, data?.error || `Request failed (${res.status}).`);
  return data;
}

/* ---------------------------------------------------------------- session */

function setSession(token, user) {
  state.token = token;
  state.user = user;
  localStorage.setItem("shopsphere.token", token);
  localStorage.setItem("shopsphere.user", JSON.stringify(user));
  applySessionUI();
}

function signOut(expired = false) {
  state.token = null;
  state.user = null;
  localStorage.removeItem("shopsphere.token");
  localStorage.removeItem("shopsphere.user");
  applySessionUI();
  updateCartBadge(0);
  if (expired) toast("Your session expired. Please sign in again.", "warn");
  else toast("You have been signed out.", "info");
  if (location.hash !== "#/shop" && location.hash !== "") location.hash = "#/shop";
  else router();
}

function applySessionUI() {
  const signedIn = Boolean(state.token && state.user);
  document.body.classList.toggle("signed-out", !signedIn);
  $("#whoami").textContent = signedIn ? `Hi, ${state.user.name.split(" ")[0]}` : "";
  if (signedIn) refreshCartBadge();
}

function requireLogin() {
  if (!state.token) {
    location.hash = "#/login";
    throw new ApiError(401, "Sign in to continue.");
  }
}

function updateCartBadge(count) {
  const badge = $("#cart-count");
  badge.textContent = count;
  badge.hidden = !count;
}

async function refreshCartBadge() {
  if (!state.token) return updateCartBadge(0);
  try {
    const cart = await api("/api/cart", { auth: true });
    updateCartBadge(cart.item_count);
  } catch {
    /* badge is cosmetic; ignore failures */
  }
}

/* ---------------------------------------------------------------- toasts */

function toast(message, kind = "info") {
  const el = document.createElement("div");
  el.className = `toast ${kind}`;
  el.textContent = message;
  $("#toasts").append(el);
  setTimeout(() => el.remove(), 4000);
}

/* ---------------------------------------------------------------- router */

const protectedRoutes = new Set(["for-you", "cart", "orders", "notifications", "admin"]);
const views = {
  shop: renderShop,
  "for-you": renderForYou,
  cart: renderCart,
  orders: renderOrders,
  notifications: renderNotifications,
  admin: renderAdmin,
  login: renderLogin,
};

async function router() {
  const name = (location.hash.replace(/^#\/?/, "").split("?")[0]) || "shop";
  const view = $("#view");

  if (protectedRoutes.has(name) && !state.token) {
    location.hash = "#/login";
    toast("Please sign in to open that page.", "warn");
    return;
  }

  document.querySelectorAll(".nav a[data-route]").forEach((a) => {
    a.classList.toggle("active", a.dataset.route === name);
  });

  const render = views[name] || renderShop;
  view.innerHTML = `<div class="loading">Loading…</div>`;
  try {
    await render(view);
  } catch (err) {
    view.innerHTML = `
      <div class="empty">
        <h2>Something went wrong</h2>
        <p>${esc(err.message)}</p>
        <a class="btn" href="#/shop">Back to shop</a>
      </div>`;
  }
  window.scrollTo({ top: 0 });
}

window.addEventListener("hashchange", router);

/* ---------------------------------------------------------------- views: shop */

function productCard(p) {
  const stock = p.stock === 0
    ? `<span class="pill out">Out of stock</span>`
    : p.stock <= 5
      ? `<span class="pill low">Only ${p.stock} left</span>`
      : `<span class="pill ok">In stock · ${p.stock}</span>`;
  return `
    <article class="card product">
      <div class="product-emoji" aria-hidden="true">${esc(p.emoji)}</div>
      <div class="product-body">
        <span class="category">${esc(cap(p.category))}</span>
        <h3>${esc(p.name)}</h3>
        <p>${esc(p.description)}</p>
      </div>
      <div class="product-foot">
        <div><div class="price">${money.format(p.price)}</div>${stock}</div>
        <button class="btn btn-primary" data-action="add" data-id="${p.id}" ${p.stock === 0 ? "disabled" : ""}>
          Add to cart
        </button>
      </div>
    </article>`;
}

async function renderShop(view) {
  const categories = await api("/api/categories");
  const { category, q } = state.filters;

  view.innerHTML = `
    <section class="hero">
      <div>
        <h1>Shop the catalogue</h1>
        <p class="muted">Stock is checked and reserved in a single database transaction at checkout,
          so two shoppers can never buy the last unit.</p>
      </div>
    </section>
    <div class="toolbar">
      <input id="search" type="search" placeholder="Search products…" value="${esc(q)}" aria-label="Search products" />
      <div class="chips" id="chips">
        ${["", ...categories].map((c) => `
          <button class="chip ${c === category ? "on" : ""}" data-cat="${esc(c)}">${c ? esc(cap(c)) : "All"}</button>
        `).join("")}
      </div>
    </div>
    <div id="grid" class="grid"></div>`;

  $("#chips").addEventListener("click", (e) => {
    const chip = e.target.closest("[data-cat]");
    if (!chip) return;
    state.filters.category = chip.dataset.cat;
    renderShop(view);
  });

  let debounce;
  $("#search").addEventListener("input", (e) => {
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      state.filters.q = e.target.value.trim();
      loadProducts();
    }, 250);
  });

  await loadProducts();
}

async function loadProducts() {
  const params = new URLSearchParams();
  if (state.filters.category) params.set("category", state.filters.category);
  if (state.filters.q) params.set("q", state.filters.q);
  const products = await api(`/api/products?${params}`);
  const grid = $("#grid");
  if (!grid) return;
  grid.innerHTML = products.length
    ? products.map(productCard).join("")
    : `<div class="empty" style="grid-column:1/-1"><h2>No products match</h2><p>Try another search or category.</p></div>`;
}

/* ---------------------------------------------------------------- views: recommendations */

async function renderForYou(view) {
  const { recommendations } = await api("/api/recommendations", { auth: true });
  view.innerHTML = `
    <h1>Recommended for you</h1>
    <p class="muted">Products that shoppers often buy together with items from your past orders.
      The Python recommendation worker updates these counts after every order.</p>
    ${recommendations.length
      ? `<div class="grid" style="margin-top:1.2rem">${recommendations.map(productCard).join("")}</div>`
      : `<div class="empty" style="margin-top:1.2rem">
           <h2>No suggestions yet</h2>
           <p>Place an order with a few items and recommendations will show up here.</p>
           <a class="btn btn-primary" href="#/shop">Go shopping</a>
         </div>`}`;
}

/* ---------------------------------------------------------------- views: cart */

function cartRow(item) {
  return `
    <div class="card cart-row">
      <div class="product-emoji small" aria-hidden="true">${esc(item.emoji)}</div>
      <div class="grow">
        <h3>${esc(item.name)}</h3>
        <p class="muted small">${money.format(item.price)} each · ${item.stock} in stock</p>
      </div>
      <div class="qty">
        <button class="btn btn-icon" data-action="qty" data-id="${item.product_id}" data-delta="-1" aria-label="Decrease">−</button>
        <span class="qty-value">${item.quantity}</span>
        <button class="btn btn-icon" data-action="qty" data-id="${item.product_id}" data-delta="1"
          aria-label="Increase" ${item.quantity >= item.stock ? "disabled" : ""}>+</button>
      </div>
      <div class="line-total">${money.format(item.line_total)}</div>
      <button class="btn btn-ghost btn-danger" data-action="remove" data-id="${item.product_id}">Remove</button>
    </div>`;
}

async function renderCart(view) {
  const cart = await api("/api/cart", { auth: true });
  updateCartBadge(cart.item_count);

  if (!cart.items.length) {
    view.innerHTML = `
      <div class="empty">
        <h2>Your cart is empty</h2>
        <p>Add something from the catalogue to get started.</p>
        <a class="btn btn-primary" href="#/shop">Browse products</a>
      </div>`;
    return;
  }

  view.innerHTML = `
    <h1>Your cart</h1>
    <div class="cart-layout" style="margin-top:1rem">
      <div class="list">${cart.items.map(cartRow).join("")}</div>
      <aside class="card summary">
        <h3>Order summary</h3>
        <div class="row"><span>Items</span><span>${cart.item_count}</span></div>
        <div class="row total"><span>Total</span><span>${money.format(cart.total)}</span></div>
        <p class="muted small">Payment is simulated for this demo. Stock is verified and reserved when you place the order.</p>
        <button class="btn btn-primary btn-block" data-action="checkout" id="checkout-btn">Place order</button>
      </aside>
    </div>`;
}

/* ---------------------------------------------------------------- views: orders */

function orderCard(o) {
  return `
    <article class="card order">
      <header>
        <div>
          <h3>Order #${o.id}</h3>
          <p class="muted small">${fmtDate(o.created_at)}</p>
        </div>
        <span class="pill ok">${esc(cap(o.status.toLowerCase()))}</span>
      </header>
      <ul class="order-items">
        ${o.items.map((i) => `
          <li><span>${esc(i.product_name)} × ${i.quantity}</span><span>${money.format(i.line_total)}</span></li>
        `).join("")}
      </ul>
      <footer>
        <span class="muted small">${o.payment_status === "PAID_SIMULATED" ? "Paid (simulated)" : esc(cap(o.payment_status.replace(/_/g, " ").toLowerCase()))}</span>
        <div class="order-total">
          <strong>${money.format(o.total_amount)}</strong>
          <button class="btn" data-action="invoice" data-id="${o.id}">Download invoice</button>
        </div>
      </footer>
    </article>`;
}

async function renderOrders(view) {
  const orders = await api("/api/orders", { auth: true });
  view.innerHTML = `
    <h1>Your orders</h1>
    <p class="muted">Invoices are rendered by the Python invoice worker a moment after checkout.</p>
    ${orders.length
      ? `<div class="stack">${orders.map(orderCard).join("")}</div>`
      : `<div class="empty" style="margin-top:1.2rem">
           <h2>No orders yet</h2>
           <p>Completed checkouts will appear here.</p>
           <a class="btn btn-primary" href="#/shop">Start shopping</a>
         </div>`}`;
}

async function downloadInvoice(orderId, button) {
  const label = button.textContent;
  button.disabled = true;
  button.textContent = "Preparing…";
  try {
    for (let attempt = 0; attempt < 15; attempt++) {
      const res = await api(`/api/orders/${orderId}/invoice`, { auth: true, raw: true });
      if (res.status === 200) {
        const url = URL.createObjectURL(await res.blob());
        const a = Object.assign(document.createElement("a"), { href: url, download: `invoice_${orderId}.pdf` });
        document.body.append(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 10000);
        toast("Invoice downloaded.", "ok");
        return;
      }
      if (res.status !== 202) throw new ApiError(res.status, `Invoice unavailable (${res.status}).`);
      await sleep(1000);
    }
    toast("The invoice is still being generated. Try again in a moment.", "warn");
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
}

/* ---------------------------------------------------------------- views: notifications */

async function renderNotifications(view) {
  const items = await api("/api/notifications", { auth: true });
  view.innerHTML = `
    <h1>Notifications</h1>
    <p class="muted">Recorded by the Python notification worker each time an order is confirmed.</p>
    ${items.length
      ? `<ul class="feed">${items.map((n) => `
          <li class="card">
            <span class="dot" aria-hidden="true"></span>
            <div>
              <p>${esc(n.message)}</p>
              <span class="muted small">${fmtDate(n.created_at)} · Order #${n.order_id}</span>
            </div>
          </li>`).join("")}</ul>`
      : `<div class="empty" style="margin-top:1rem">
           <h2>No notifications yet</h2>
           <p>Place an order and the worker will log a confirmation here.</p>
         </div>`}`;
}

/* ---------------------------------------------------------------- views: login */

function renderLogin(view) {
  if (state.token) {
    location.hash = "#/shop";
    return;
  }
  view.innerHTML = `
    <div class="card auth-card">
      <div class="tabs" role="tablist">
        <button class="tab on" data-mode="login" role="tab">Sign in</button>
        <button class="tab" data-mode="register" role="tab">Create account</button>
      </div>
      <form id="auth-form" class="form">
        <label class="field" data-register hidden>Name
          <input name="name" autocomplete="name" maxlength="100" />
        </label>
        <label class="field">Email
          <input name="email" type="email" autocomplete="email" required />
        </label>
        <label class="field">Password
          <input name="password" type="password" minlength="8" maxlength="128" required autocomplete="current-password" />
        </label>
        <button class="btn btn-primary btn-block" type="submit" id="auth-submit">Sign in</button>
        <p class="muted small">Passwords are hashed with PBKDF2 on the server. Use at least 8 characters.</p>
      </form>
    </div>`;

  let mode = "login";
  const form = $("#auth-form");
  view.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      mode = tab.dataset.mode;
      view.querySelectorAll(".tab").forEach((t) => t.classList.toggle("on", t === tab));
      const isRegister = mode === "register";
      form.querySelector("[data-register]").hidden = !isRegister;
      form.querySelector("[name=name]").required = isRegister;
      form.querySelector("[name=password]").autocomplete = isRegister ? "new-password" : "current-password";
      $("#auth-submit").textContent = isRegister ? "Create account" : "Sign in";
    });
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const payload = Object.fromEntries(new FormData(form));
    if (mode === "login") delete payload.name;
    const button = $("#auth-submit");
    button.disabled = true;
    try {
      const res = await api(mode === "login" ? "/api/auth/login" : "/api/auth/register", {
        method: "POST",
        body: payload,
      });
      setSession(res.token, res.user);
      toast(`Welcome, ${res.user.name}!`, "ok");
      location.hash = "#/shop";
    } catch (err) {
      toast(err.message, "error");
    } finally {
      button.disabled = false;
    }
  });
}

/* ---------------------------------------------------------------- views: admin */

async function renderAdmin(view) {
  const [levels, categories] = await Promise.all([
    api("/api/inventory"),
    api("/api/categories"),
  ]);
  view.innerHTML = `
    <h1>Admin</h1>
    <p class="muted">Create products and manage stock. Changes go straight to PostgreSQL through the product and inventory services.</p>
    <div class="admin-grid">
      <section class="card">
        <h3>Add product</h3>
        <form id="product-form" class="form" style="margin-top:0.8rem">
          <label class="field">Name
            <input name="name" required maxlength="150" />
          </label>
          <label class="field">Description
            <textarea name="description" rows="2" maxlength="1000"></textarea>
          </label>
          <div class="two">
            <label class="field">Price (₹)
              <input name="price" type="number" step="0.01" min="0" required />
            </label>
            <label class="field">Opening stock
              <input name="stock" type="number" min="0" value="10" required />
            </label>
          </div>
          <div class="two">
            <label class="field">Category
              <input name="category" list="category-list" required maxlength="50" />
            </label>
            <label class="field">Icon (emoji)
              <input name="emoji" maxlength="16" value="📦" />
            </label>
          </div>
          <datalist id="category-list">
            ${categories.map((c) => `<option value="${esc(c)}"></option>`).join("")}
          </datalist>
          <button class="btn btn-primary" type="submit">Create product</button>
        </form>
      </section>

      <section class="card">
        <h3>Stock levels</h3>
        <div style="overflow-x:auto">
          <table class="table">
            <thead><tr><th>ID</th><th>Product</th><th>Stock</th></tr></thead>
            <tbody>
              ${levels.map((l) => `
                <tr><td>${l.product_id}</td><td>${esc(l.product_name)}</td>
                    <td><strong>${l.stock_quantity}</strong></td></tr>`).join("")}
            </tbody>
          </table>
        </div>
        <form id="restock-form" class="form inline">
          <input name="product_id" type="number" min="1" placeholder="Product ID" required />
          <input name="stock_quantity" type="number" min="0" placeholder="New stock level" required />
          <button class="btn" type="submit">Set stock</button>
        </form>
      </section>
    </div>`;

  $("#product-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = Object.fromEntries(new FormData(e.currentTarget));
    try {
      await api("/api/products", {
        method: "POST",
        auth: true,
        body: {
          name: f.name,
          description: f.description,
          price: Number(f.price),
          category: f.category,
          emoji: f.emoji,
          stock: Number(f.stock),
        },
      });
      toast("Product created.", "ok");
      renderAdmin(view);
    } catch (err) {
      toast(err.message, "error");
    }
  });

  $("#restock-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = Object.fromEntries(new FormData(e.currentTarget));
    try {
      await api(`/api/inventory/${Number(f.product_id)}`, {
        method: "PUT",
        auth: true,
        body: { stock_quantity: Number(f.stock_quantity) },
      });
      toast("Stock updated.", "ok");
      renderAdmin(view);
    } catch (err) {
      toast(err.message, "error");
    }
  });
}

/* ---------------------------------------------------------------- actions */

async function addToCart(productId, quantity) {
  const cart = await api("/api/cart", {
    method: "POST",
    auth: true,
    body: { product_id: productId, quantity },
  });
  updateCartBadge(cart.item_count);
}

async function setCartQuantity(productId, quantity) {
  await api(`/api/cart/${productId}`, { method: "PUT", auth: true, body: { quantity } });
}

document.addEventListener("click", async (event) => {
  const el = event.target.closest("[data-action]");
  if (!el || el.disabled) return;
  const { action, id, delta } = el.dataset;

  try {
    switch (action) {
      case "add":
        requireLogin();
        await addToCart(Number(id), 1);
        toast("Added to your cart.", "ok");
        break;

      case "qty": {
        const current = Number(el.parentElement.querySelector(".qty-value").textContent);
        await setCartQuantity(Number(id), current + Number(delta));
        router();
        break;
      }

      case "remove":
        await api(`/api/cart/${id}`, { method: "DELETE", auth: true });
        toast("Removed from cart.", "info");
        router();
        break;

      case "checkout": {
        const result = await api("/api/checkout", { method: "POST", auth: true });
        toast(`Order #${result.order?.id ?? result.order_id} placed!`, "ok");
        updateCartBadge(0);
        location.hash = "#/orders"; // the hashchange listener renders the page
        break;
      }

      case "invoice":
        await downloadInvoice(id, el);
        break;

      case "logout":
        signOut();
        break;
    }
  } catch (err) {
    toast(err.message, "error");
  }
});

/* ---------------------------------------------------------------- boot */

applySessionUI();
router();
