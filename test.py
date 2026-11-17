#!/usr/bin/env python3
"""
Model 1: Multi-Timeframe Volume- and Open-Interest-Weighted KDE (KDE-VPVR-OI)
Objective: Mapping Support, Resistance, Equilibrium (POC/VAH/VAL), and LVN Voids.

Ingests Spot OHLCV, Perpetual Swap OHLCV, and Open Interest via CCXT
(Binance, Bybit, OKX) with automatic fallback to a deterministic 180-day
multi-regime synthetic 5m-resampled simulation.
"""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from typing import Dict, List, Literal, Optional, Tuple

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from scipy.signal import find_peaks

try:
    import ccxt
    CCXT_AVAILABLE = True
except ImportError:
    ccxt = None  # type: ignore[assignment]
    CCXT_AVAILABLE = False

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger("kde_vpvr_oi")


# ---------------------------------------------------------------------------
# 1. Configuration & Structural Data Containers
# ---------------------------------------------------------------------------
@dataclass
class TimeframeSpec:
    timeframe: str
    lookback_bars: int
    weight: float
    resample_rule: str


@dataclass
class Model1Config:
    exchange_id: Literal["binance", "bybit", "okx"] = "bybit"
    spot_symbol: str = "BTC/USDT"
    perp_symbol: str = "BTC/USDT:USDT"
    display_timeframe: str = "4h"
    display_candles: int = 180
    # Model 1 mathematical constants
    lam: float = 0.0002
    alpha_oi: float = 0.45
    grid_points: int = 150
    gamma_va: float = 0.70
    silverman_cap: float = 0.65
    atr_period: int = 14
    atr_cap: float = 1.25
    grid_floor: float = 1.5
    hvn_prominence_ratio: float = 0.05
    lvn_prominence_ratio: float = 0.03
    hvn_half_width_mult: float = 0.85
    # Adaptation & runtime toggles
    weighted_spread: bool = True
    atr_mode: Literal["tf", "5m"] = "tf"
    include_boundary_extrema: bool = False
    unified_grid_points: int = 250
    synthetic_fallback: bool = True
    synthetic_seed: int = 123
    output_image_path: str = "kde_vpvr_oi_mtf_sr.png"
    timeframes: List[TimeframeSpec] = field(default_factory=lambda: [
        TimeframeSpec("15m", 600, 0.15, "15min"),
        TimeframeSpec("1h", 720, 0.25, "1h"),
        TimeframeSpec("4h", 540, 0.30, "4h"),
        TimeframeSpec("1d", 180, 0.30, "1D"),
    ])


@dataclass
class HVNZone:
    price: float
    low: float
    high: float
    density: float
    prominence: float
    timeframe: str


@dataclass
class LVNZone:
    price: float
    density: float
    prominence: float
    timeframe: str


@dataclass
class ProfileResult:
    timeframe: str
    grid: np.ndarray
    f_hat: np.ndarray
    f_tilde: np.ndarray
    weights: np.ndarray
    n_bars: int
    n_eff: float
    h_silverman: float
    med_atr: float
    delta_g: float
    h: float
    poc: float
    val: float
    vah: float
    va_mass: float
    hvns: List[HVNZone]
    lvns: List[LVNZone]


@dataclass
class MTFSRZone:
    center: float
    low: float
    high: float
    timeframes: List[str]
    n_tf: int
    score: float
    role: Literal["Support", "Resistance", "Active Equilibrium"]


# ---------------------------------------------------------------------------
# 2. CCXT Multi-Timeframe Data Ingestion & Synthetic Fallback
# ---------------------------------------------------------------------------
TIMEFRAME_MS: Dict[str, int] = {
    "1m": 60_000,
    "3m": 180_000,
    "5m": 300_000,
    "15m": 900_000,
    "30m": 1_800_000,
    "1h": 3_600_000,
    "2h": 7_200_000,
    "4h": 14_400_000,
    "1d": 86_400_000,
}


class CCXTMarketDataFetcher:
    def __init__(self, exchange_id: str) -> None:
        if not CCXT_AVAILABLE:
            raise RuntimeError("ccxt library is not installed.")
        exchange_cls = getattr(ccxt, exchange_id)
        self.exchange_id = exchange_id
        self.exchange = exchange_cls({"enableRateLimit": True})
        self.markets = self.exchange.load_markets()

    def _perp_volume_multiplier(self, perp_symbol: str) -> Tuple[float, bool]:
        """Return (contract_size, is_inverse) to normalize perp volume to base units."""
        mkt = self.markets.get(perp_symbol, {})
        is_contract = bool(mkt.get("contract", False))
        is_inverse = bool(mkt.get("inverse", False))
        contract_size = float(mkt.get("contractSize") or 1.0)
        if not is_contract:
            return 1.0, False
        return contract_size, is_inverse

    def fetch_ohlcv_paginated(
        self, symbol: str, timeframe: str, lookback_bars: int
    ) -> pd.DataFrame:
        if not self.exchange.has.get("fetchOHLCV", False):
            raise RuntimeError(f"{self.exchange_id} does not support fetchOHLCV")

        tf_ms = TIMEFRAME_MS[timeframe]
        now_ms = self.exchange.milliseconds()
        since_ms = now_ms - (lookback_bars + 5) * tf_ms
        per_call_limit = 1000 if self.exchange_id in ("binance", "bybit") else 300

        rows: List[List[float]] = []
        cursor = since_ms
        while len(rows) < lookback_bars + 5 and cursor < now_ms:
            batch = self.exchange.fetch_ohlcv(
                symbol, timeframe=timeframe, since=cursor, limit=per_call_limit
            )
            if not batch:
                break
            rows.extend(batch)
            last_ts = int(batch[-1][0])
            if last_ts <= cursor:
                break
            cursor = last_ts + tf_ms
            if len(batch) < per_call_limit:
                break
            time.sleep(max(self.exchange.rateLimit / 1000.0, 0.05))

        if not rows:
            raise RuntimeError(f"Empty OHLCV returned for {symbol} {timeframe}")

        df = pd.DataFrame(
            rows, columns=["timestamp", "open", "high", "low", "close", "volume"]
        )
        df = df.drop_duplicates(subset=["timestamp"]).sort_values("timestamp")
        df["timestamp"] = pd.to_datetime(df["timestamp"], unit="ms", utc=True)
        df = df.set_index("timestamp").tail(lookback_bars)
        return df.astype(float)

    def fetch_oi_history_paginated(
        self, perp_symbol: str, timeframe: str, lookback_bars: int
    ) -> pd.Series:
        if not self.exchange.has.get("fetchOpenInterestHistory", False):
            logger.warning("%s lacks fetchOpenInterestHistory; defaulting OI=0", self.exchange_id)
            return pd.Series(dtype=float)

        tf_ms = TIMEFRAME_MS[timeframe]
        now_ms = self.exchange.milliseconds()
        earliest_ms = now_ms - (lookback_bars + 5) * tf_ms

        oi_symbol = perp_symbol
        if self.exchange_id == "okx":
            # OKX unified OI history expects base currency code (e.g., 'BTC')
            oi_symbol = perp_symbol.split("/")[0]
            per_call = 100
        elif self.exchange_id == "bybit":
            per_call = 200
        else:
            per_call = 500

        records: List[dict] = []
        until_ms = now_ms
        prev_oldest: Optional[int] = None
        for _ in range(max(1, (lookback_bars // per_call) + 3)):
            params = {"until": until_ms} if until_ms < now_ms else {}
            try:
                batch = self.exchange.fetch_open_interest_history(
                    oi_symbol,
                    timeframe=timeframe,
                    since=earliest_ms,
                    limit=per_call,
                    params=params,
                )
            except Exception as exc:
                logger.warning("OI fetch stopped early (%s %s): %s", perp_symbol, timeframe, exc)
                break
            if not batch:
                break
            records.extend(batch)
            ts_list = [int(r["timestamp"]) for r in batch if r.get("timestamp") is not None]
            if not ts_list:
                break
            oldest_ts = min(ts_list)
            if (prev_oldest is not None and oldest_ts >= prev_oldest) or oldest_ts <= earliest_ms or len(batch) < per_call:
                break
            prev_oldest = oldest_ts
            next_until = oldest_ts - tf_ms
            if next_until >= until_ms:
                break
            until_ms = next_until
            time.sleep(max(self.exchange.rateLimit / 1000.0, 0.05))

        if not records:
            return pd.Series(dtype=float)

        contract_size, is_inverse = self._perp_volume_multiplier(perp_symbol)
        parsed = []
        for r in records:
            ts = r.get("timestamp")
            amt = r.get("openInterestAmount")
            val = r.get("openInterestValue")
            if ts is None:
                continue
            if amt is not None:
                oi_base = float(amt) * (1.0 if self.exchange_id == "binance" else contract_size)
            elif val is not None and not is_inverse:
                oi_base = float(val)
            else:
                continue
            parsed.append((int(ts), oi_base))

        if not parsed:
            return pd.Series(dtype=float)

        oi_df = pd.DataFrame(parsed, columns=["timestamp", "open_interest"])
        oi_df = oi_df.drop_duplicates(subset=["timestamp"]).sort_values("timestamp")
        oi_df["timestamp"] = pd.to_datetime(oi_df["timestamp"], unit="ms", utc=True)
        return oi_df.set_index("timestamp")["open_interest"]

    def fetch_aligned_timeframe(self, cfg: Model1Config, spec: TimeframeSpec) -> pd.DataFrame:
        spot_df = self.fetch_ohlcv_paginated(cfg.spot_symbol, spec.timeframe, spec.lookback_bars)
        perp_df = self.fetch_ohlcv_paginated(cfg.perp_symbol, spec.timeframe, spec.lookback_bars)
        oi_series = self.fetch_oi_history_paginated(
            cfg.perp_symbol, spec.timeframe, spec.lookback_bars
        )

        contract_size, is_inverse = self._perp_volume_multiplier(cfg.perp_symbol)
        aligned = spot_df[["open", "high", "low", "close"]].copy()
        aligned["vol_spot"] = spot_df["volume"]

        # Scale contract volume into base units via market['contractSize']
        perp_vol = perp_df["volume"].reindex(aligned.index).fillna(0.0)
        if is_inverse:
            typical_p = ((aligned["high"] + aligned["low"] + aligned["close"]) / 3.0).clip(lower=1e-9)
            aligned["vol_perp"] = (perp_vol * contract_size) / typical_p
        else:
            aligned["vol_perp"] = perp_vol * contract_size

        # Align OI timestamps without backfilling pre-retention gaps to prevent delta spikes
        if len(oi_series) > 0:
            tol = pd.Timedelta(milliseconds=TIMEFRAME_MS[spec.timeframe] // 2)
            aligned_oi = pd.merge_asof(
                pd.DataFrame({"ts": aligned.index}),
                oi_series.rename("open_interest").reset_index().rename(columns={"timestamp": "ts"}),
                on="ts",
                direction="nearest",
                tolerance=tol,
            ).set_index("ts")["open_interest"]
            aligned["open_interest"] = aligned_oi
        else:
            aligned["open_interest"] = np.nan

        return aligned


def generate_synthetic_mtf_data(
    cfg: Model1Config,
) -> Tuple[Dict[str, pd.DataFrame], pd.DataFrame]:
    """
    Generate a coherent 180-day 5m synthetic Spot + Perp + OI dataset
    and resample identically across all configured timeframes.
    """
    rng = np.random.default_rng(cfg.synthetic_seed)
    n_5m = 180 * 24 * 12  # 51,840 bars
    t_5m = pd.date_range("2026-05-20", periods=n_5m, freq="5min", tz="UTC")

    segments = [
        (0, 9000, 61500.0, 550.0),
        (9000, 9400, 64800.0, 300.0),
        (9400, 20000, 64800.0, 650.0),
        (20000, 20500, 67500.0, 300.0),
        (20500, 31000, 67500.0, 700.0),
        (31000, 31500, 71200.0, 320.0),
        (31500, 41000, 71200.0, 680.0),
        (41000, 41400, 74000.0, 350.0),
        (41400, 46500, 74000.0, 600.0),
        (46500, 47000, 67500.0, 400.0),
        (47000, n_5m, 67800.0, 480.0),
    ]
    target = np.zeros(n_5m, dtype=float)
    vol_scale = np.zeros(n_5m, dtype=float)
    in_transition = np.zeros(n_5m, dtype=bool)
    for i, (s, e, lvl, std) in enumerate(segments):
        if i > 0 and (e - s) <= 600:
            target[s:e] = np.linspace(segments[i - 1][2], lvl, e - s)
            vol_scale[s:e] = std
            in_transition[s:e] = True
        else:
            target[s:e] = lvl
            vol_scale[s:e] = std

    closes = np.zeros(n_5m, dtype=float)
    closes[0] = target[0]
    theta = 0.04
    for i in range(1, n_5m):
        sigma = vol_scale[i] * np.sqrt(2.0 * theta)
        closes[i] = closes[i - 1] + theta * (target[i] - closes[i - 1]) + rng.normal(0.0, sigma)

    opens = np.roll(closes, 1)
    opens[0] = closes[0]
    wick = np.abs(rng.normal(0.0, 35.0, n_5m)) + 15.0
    highs = np.maximum(opens, closes) + wick * rng.uniform(0.3, 1.0, n_5m)
    lows = np.minimum(opens, closes) - wick * rng.uniform(0.3, 1.0, n_5m)

    shelf_touch = (
        (np.abs(closes - 67500.0) < 250.0)
        | (np.abs(closes - 71200.0) < 250.0)
        | (np.abs(closes - 64800.0) < 250.0)
    )
    vol_spot = (
        rng.lognormal(3.6, 0.55, n_5m)
        * np.where(in_transition, 0.45, 1.35)
        * np.where(shelf_touch, 1.45, 1.0)
    )
    vol_perp = (
        rng.lognormal(4.5, 0.60, n_5m)
        * np.where(in_transition, 0.55, 1.50)
        * np.where(shelf_touch, 1.65, 1.0)
    )
    oi_changes = np.where(shelf_touch, rng.normal(8.0, 130.0, n_5m), rng.normal(0.0, 45.0, n_5m))
    oi = np.maximum(20000.0, 95000.0 + np.cumsum(oi_changes))

    df_5m = pd.DataFrame(
        {
            "open": opens,
            "high": highs,
            "low": lows,
            "close": closes,
            "vol_spot": vol_spot,
            "vol_perp": vol_perp,
            "open_interest": oi,
        },
        index=t_5m,
    )

    agg_map = {
        "open": "first",
        "high": "max",
        "low": "min",
        "close": "last",
        "vol_spot": "sum",
        "vol_perp": "sum",
        "open_interest": "last",
    }
    tf_dfs: Dict[str, pd.DataFrame] = {}
    for spec in cfg.timeframes:
        if spec.timeframe == "5m":
            tf_dfs[spec.timeframe] = df_5m.tail(spec.lookback_bars).copy()
        else:
            tf_dfs[spec.timeframe] = (
                df_5m.resample(spec.resample_rule).agg(agg_map).dropna().tail(spec.lookback_bars)
            )
    return tf_dfs, df_5m


# ---------------------------------------------------------------------------
# 3. Core Model 1 Engine (KDE-VPVR-OI)
# ---------------------------------------------------------------------------
def compute_atr(df: pd.DataFrame, period: int = 14) -> pd.Series:
    h = df["high"].to_numpy(dtype=float)
    l = df["low"].to_numpy(dtype=float)
    c = df["close"].to_numpy(dtype=float)
    pc = np.empty_like(c)
    pc[0] = c[0]
    pc[1:] = c[:-1]
    tr = np.maximum(h - l, np.maximum(np.abs(h - pc), np.abs(l - pc)))
    return pd.Series(tr, index=df.index).ewm(alpha=1.0 / period, adjust=False).mean()


def weighted_quantile(values: np.ndarray, quantiles: np.ndarray, weights: np.ndarray) -> np.ndarray:
    sorter = np.argsort(values)
    v_sorted = values[sorter]
    w_sorted = weights[sorter]
    w_sum = float(np.sum(w_sorted))
    if w_sum <= 0.0:
        return np.quantile(values, quantiles)
    cdf = (np.cumsum(w_sorted) - 0.5 * w_sorted) / w_sum
    return np.interp(quantiles, cdf, v_sorted)


def compute_kde_vpvr_oi(
    df: pd.DataFrame,
    timeframe: str,
    cfg: Model1Config,
    atr_5m_series: Optional[pd.Series] = None,
) -> ProfileResult:
    if len(df) < 3:
        raise ValueError(f"Insufficient bars ({len(df)}) for KDE-VPVR-OI on {timeframe}")

    p = ((df["high"] + df["low"] + df["close"]) / 3.0).to_numpy(dtype=float)
    v = np.nan_to_num((df["vol_spot"] + df["vol_perp"]).to_numpy(dtype=float), nan=0.0)
    v = np.clip(v, 0.0, None)

    # Compute |Delta OI_i| across consecutive non-NaN OI bars so retention gaps do not spike
    oi_series = df["open_interest"].astype(float)
    d_oi = oi_series.diff().abs().fillna(0.0).to_numpy(dtype=float)

    n_bars = len(p)
    idx = np.arange(1, n_bars + 1, dtype=float)
    w_time = np.exp(-cfg.lam * (n_bars - idx))

    v_mean = float(np.mean(v))
    v_tilde = v / v_mean if v_mean > 0.0 else np.ones(n_bars, dtype=float)

    doi_mean = float(np.mean(d_oi))
    doi_tilde = d_oi / doi_mean if doi_mean > 0.0 else np.zeros(n_bars, dtype=float)

    raw_w = w_time * v_tilde * (1.0 + cfg.alpha_oi * doi_tilde)
    raw_sum = float(np.sum(raw_w))
    w = raw_w / raw_sum if raw_sum > 0.0 else np.full(n_bars, 1.0 / n_bars, dtype=float)

    # Kish's effective sample size
    n_eff = float((np.sum(w) ** 2) / np.sum(w ** 2))

    if cfg.weighted_spread:
        mu = float(np.sum(w * p))
        denom = 1.0 - float(np.sum(w ** 2))
        var_w = float(np.sum(w * (p - mu) ** 2)) / denom if denom > 1e-12 else float(np.var(p))
        sigma_p = float(np.sqrt(max(var_w, 0.0)))
        q25, q75 = weighted_quantile(p, np.array([0.25, 0.75]), w)
        iqr_p = float(q75 - q25)
    else:
        sigma_p = float(np.std(p, ddof=1))
        q25, q75 = np.quantile(p, [0.25, 0.75])
        iqr_p = float(q75 - q25)

    p_min, p_max = float(np.min(p)), float(np.max(p))
    if p_max <= p_min:
        eps = max(abs(p_min) * 1e-4, 1e-6)
        p_min, p_max = p_min - eps, p_max + eps

    delta_g = (p_max - p_min) / cfg.grid_points
    if iqr_p > 0.0 and sigma_p > 0.0:
        spread = min(sigma_p, iqr_p / 1.34)
    elif sigma_p > 0.0:
        spread = sigma_p
    else:
        spread = max(delta_g, 1e-6)

    h_silverman = 0.9 * spread * (n_eff ** (-0.2))

    if cfg.atr_mode == "5m" and atr_5m_series is not None and len(atr_5m_series) > 0:
        atr_ref = atr_5m_series
    else:
        atr_ref = compute_atr(df, period=cfg.atr_period)

    med_atr = float(np.nanmedian(atr_ref.to_numpy(dtype=float)))
    if not np.isfinite(med_atr) or med_atr <= 0.0:
        med_atr = delta_g

    h = max(cfg.grid_floor * delta_g, min(cfg.silverman_cap * h_silverman, cfg.atr_cap * med_atr))

    # Vectorized Gaussian KDE evaluation
    grid = np.linspace(p_min, p_max, cfg.grid_points)
    z = (grid[:, None] - p[None, :]) / h
    f_hat = np.sum(w[None, :] * (1.0 / (np.sqrt(2.0 * np.pi) * h)) * np.exp(-0.5 * z ** 2), axis=1)
    f_sum = float(np.sum(f_hat))
    f_tilde = f_hat / f_sum if f_sum > 0.0 else np.full(cfg.grid_points, 1.0 / cfg.grid_points)

    # Point of Control
    m_poc = int(np.argmax(f_tilde))
    poc = float(grid[m_poc])

    # Greedy bilevel outward Value Area expansion (70% probability mass)
    m_low, m_high = m_poc, m_poc
    va_mass = float(f_tilde[m_poc])
    while va_mass < cfg.gamma_va and (m_low > 0 or m_high < cfg.grid_points - 1):
        lv = float(f_tilde[m_low - 1]) if m_low > 0 else -1.0
        rv = float(f_tilde[m_high + 1]) if m_high < cfg.grid_points - 1 else -1.0
        if lv > rv:
            m_low -= 1
            va_mass += float(f_tilde[m_low])
        elif rv > lv:
            m_high += 1
            va_mass += float(f_tilde[m_high])
        else:
            if m_low > 0:
                m_low -= 1
                va_mass += float(f_tilde[m_low])
            if m_high < cfg.grid_points - 1 and va_mass < cfg.gamma_va:
                m_high += 1
                va_mass += float(f_tilde[m_high])

    val = float(grid[m_low])
    vah = float(grid[m_high])

    # HVN Support/Resistance bands and LVN Liquidity Voids
    f_max = float(np.max(f_tilde))
    if cfg.include_boundary_extrema:
        padded = np.pad(f_tilde, 1, mode="constant", constant_values=0.0)
        hvn_idx_p, hvn_props = find_peaks(padded, prominence=cfg.hvn_prominence_ratio * f_max)
        hvn_idx = hvn_idx_p - 1
    else:
        hvn_idx, hvn_props = find_peaks(f_tilde, prominence=cfg.hvn_prominence_ratio * f_max)

    lvn_idx, lvn_props = find_peaks(-f_tilde, prominence=cfg.lvn_prominence_ratio * f_max)

    hvns = [
        HVNZone(
            price=float(grid[k]),
            low=float(grid[k] - cfg.hvn_half_width_mult * h),
            high=float(grid[k] + cfg.hvn_half_width_mult * h),
            density=float(f_tilde[k]),
            prominence=float(pr),
            timeframe=timeframe,
        )
        for k, pr in zip(hvn_idx, hvn_props["prominences"])
    ]
    lvns = [
        LVNZone(
            price=float(grid[k]),
            density=float(f_tilde[k]),
            prominence=float(pr),
            timeframe=timeframe,
        )
        for k, pr in zip(lvn_idx, lvn_props["prominences"])
    ]

    return ProfileResult(
        timeframe=timeframe,
        grid=grid,
        f_hat=f_hat,
        f_tilde=f_tilde,
        weights=w,
        n_bars=n_bars,
        n_eff=n_eff,
        h_silverman=h_silverman,
        med_atr=med_atr,
        delta_g=delta_g,
        h=h,
        poc=poc,
        val=val,
        vah=vah,
        va_mass=va_mass,
        hvns=hvns,
        lvns=lvns,
    )


# ---------------------------------------------------------------------------
# 4. Multi-Timeframe Confluence Clustering & Unified Density Fusion
# ---------------------------------------------------------------------------
def build_confluence_zones(
    profiles: Dict[str, ProfileResult],
    cfg: Model1Config,
    last_price: float,
) -> List[MTFSRZone]:
    """Strategy A: Merge overlapping per-timeframe HVN intervals into ranked S/R clusters."""
    tf_weights = {s.timeframe: s.weight for s in cfg.timeframes}
    tf_order = {s.timeframe: idx for idx, s in enumerate(cfg.timeframes)}

    items: List[Tuple[HVNZone, float]] = []
    for tf, prof in profiles.items():
        w_tf = tf_weights.get(tf, 1.0)
        for hvn in prof.hvns:
            items.append((hvn, w_tf))

    if not items:
        return []

    items.sort(key=lambda x: x[0].low)
    clusters: List[Tuple[float, float, List[Tuple[HVNZone, float]]]] = []
    cur_items = [items[0]]
    cur_low, cur_high = items[0][0].low, items[0][0].high

    for hvn, tw in items[1:]:
        if hvn.low <= cur_high:
            cur_items.append((hvn, tw))
            cur_high = max(cur_high, hvn.high)
        else:
            clusters.append((cur_low, cur_high, cur_items))
            cur_items = [(hvn, tw)]
            cur_low, cur_high = hvn.low, hvn.high
    clusters.append((cur_low, cur_high, cur_items))

    zones: List[MTFSRZone] = []
    for low_b, high_b, c_items in clusters:
        tfs = sorted(list({it[0].timeframe for it in c_items}), key=lambda t: tf_order.get(t, 99))
        score = sum(
            it[1] * (1.0 + it[0].prominence / max(float(profiles[it[0].timeframe].f_tilde.max()), 1e-12))
            for it in c_items
        )
        center = float(np.average([it[0].price for it in c_items], weights=[it[1] for it in c_items]))
        if high_b < last_price:
            role: Literal["Support", "Resistance", "Active Equilibrium"] = "Support"
        elif low_b > last_price:
            role = "Resistance"
        else:
            role = "Active Equilibrium"
        zones.append(
            MTFSRZone(
                center=center,
                low=low_b,
                high=high_b,
                timeframes=tfs,
                n_tf=len(tfs),
                score=score,
                role=role,
            )
        )

    zones.sort(key=lambda z: (z.n_tf, z.score), reverse=True)
    return zones


def fuse_mtf_profiles(
    profiles: Dict[str, ProfileResult],
    cfg: Model1Config,
) -> ProfileResult:
    """Strategy B: Convex combination of normalized continuous densities onto a unified grid."""
    tf_weights = {s.timeframe: s.weight for s in cfg.timeframes}
    g_min = min(float(p.grid[0]) for p in profiles.values())
    g_max = max(float(p.grid[-1]) for p in profiles.values())
    unified_grid = np.linspace(g_min, g_max, cfg.unified_grid_points)

    fused_pdf = np.zeros(cfg.unified_grid_points, dtype=float)
    total_w = 0.0
    for tf, prof in profiles.items():
        w_tf = tf_weights.get(tf, 1.0)
        dg_tf = float(prof.grid[1] - prof.grid[0])
        continuous_pdf = prof.f_tilde / max(dg_tf, 1e-12)
        interp_pdf = np.interp(unified_grid, prof.grid, continuous_pdf, left=0.0, right=0.0)
        fused_pdf += w_tf * interp_pdf
        total_w += w_tf

    fused_pdf /= max(total_w, 1e-12)
    f_tilde = fused_pdf / max(float(np.sum(fused_pdf)), 1e-12)

    m_poc = int(np.argmax(f_tilde))
    m_low, m_high = m_poc, m_poc
    mass = float(f_tilde[m_poc])
    while mass < cfg.gamma_va and (m_low > 0 or m_high < cfg.unified_grid_points - 1):
        lv = float(f_tilde[m_low - 1]) if m_low > 0 else -1.0
        rv = float(f_tilde[m_high + 1]) if m_high < cfg.unified_grid_points - 1 else -1.0
        if lv > rv:
            m_low -= 1
            mass += float(f_tilde[m_low])
        elif rv > lv:
            m_high += 1
            mass += float(f_tilde[m_high])
        else:
            if m_low > 0:
                m_low -= 1
                mass += float(f_tilde[m_low])
            if m_high < cfg.unified_grid_points - 1 and mass < cfg.gamma_va:
                m_high += 1
                mass += float(f_tilde[m_high])

    h_eff = float(
        np.average([p.h for p in profiles.values()], weights=[tf_weights.get(tf, 1.0) for tf in profiles])
    )
    f_max = float(np.max(f_tilde))
    hvn_idx, hvn_props = find_peaks(f_tilde, prominence=cfg.hvn_prominence_ratio * f_max)
    lvn_idx, lvn_props = find_peaks(-f_tilde, prominence=cfg.lvn_prominence_ratio * f_max)

    return ProfileResult(
        timeframe="MTF-FUSED",
        grid=unified_grid,
        f_hat=fused_pdf,
        f_tilde=f_tilde,
        weights=np.array([1.0]),
        n_bars=sum(p.n_bars for p in profiles.values()),
        n_eff=float(np.mean([p.n_eff for p in profiles.values()])),
        h_silverman=0.0,
        med_atr=0.0,
        delta_g=(g_max - g_min) / cfg.unified_grid_points,
        h=h_eff,
        poc=float(unified_grid[m_poc]),
        val=float(unified_grid[m_low]),
        vah=float(unified_grid[m_high]),
        va_mass=mass,
        hvns=[
            HVNZone(
                price=float(unified_grid[k]),
                low=float(unified_grid[k] - cfg.hvn_half_width_mult * h_eff),
                high=float(unified_grid[k] + cfg.hvn_half_width_mult * h_eff),
                density=float(f_tilde[k]),
                prominence=float(pr),
                timeframe="MTF-FUSED",
            )
            for k, pr in zip(hvn_idx, hvn_props["prominences"])
        ],
        lvns=[
            LVNZone(
                price=float(unified_grid[k]),
                density=float(f_tilde[k]),
                prominence=float(pr),
                timeframe="MTF-FUSED",
            )
            for k, pr in zip(lvn_idx, lvn_props["prominences"])
        ],
    )


# ---------------------------------------------------------------------------
# 5. Visualization (Matplotlib Dual-Panel S/R Map + Side KDE Profiles)
# ---------------------------------------------------------------------------
def plot_mtf_sr_map(
    df_plot: pd.DataFrame,
    profiles: Dict[str, ProfileResult],
    confl_zones: List[MTFSRZone],
    fused: ProfileResult,
    cfg: Model1Config,
    source_label: str,
) -> None:
    fig = plt.figure(figsize=(15, 8.5), facecolor="#0f172a")
    gs = fig.add_gridspec(1, 2, width_ratios=[3.6, 1.25], wspace=0.04)
    ax_p = fig.add_subplot(gs[0])
    ax_d = fig.add_subplot(gs[1], sharey=ax_p)

    for ax in (ax_p, ax_d):
        ax.set_facecolor("#111827")
        ax.tick_params(colors="#cbd5e1", labelsize=9)
        ax.grid(color="#1e293b", linestyle="-", linewidth=0.6, alpha=0.7)
        for sp in ax.spines.values():
            sp.set_color("#334155")

    x_idx = np.arange(len(df_plot))
    up = df_plot["close"] >= df_plot["open"]
    colors = np.where(up, "#22c55e", "#ef4444")
    ax_p.vlines(x_idx, df_plot["low"], df_plot["high"], color=colors, linewidth=0.9, alpha=0.85)
    body_h = np.where(np.abs(df_plot["close"] - df_plot["open"]) > 0, df_plot["close"] - df_plot["open"], 1e-6)
    ax_p.bar(
        x_idx,
        body_h,
        bottom=df_plot["open"],
        width=0.65,
        color=colors,
        edgecolor="none",
        alpha=0.9,
    )

    role_colors = {
        "Support": "#22d3ee",
        "Resistance": "#f59e0b",
        "Active Equilibrium": "#e879f9",
    }
    for z in confl_zones:
        col = role_colors[z.role]
        alpha_fill = 0.17 if z.n_tf >= 2 else 0.08
        ax_p.axhspan(z.low, z.high, color=col, alpha=alpha_fill)
        ax_p.axhline(z.center, color=col, linestyle="--", linewidth=1.0 if z.n_tf >= 2 else 0.7, alpha=0.8)
        tag = f"{z.role} ({'+'.join(z.timeframes)}) {z.center:,.0f}"
        ax_p.text(2, z.center, tag, color=col, fontsize=8, va="bottom", fontweight="bold")

    ax_p.axhline(fused.poc, color="#facc15", linewidth=1.7, label=f"Fused POC {fused.poc:,.0f}")
    ax_p.axhline(fused.vah, color="#a78bfa", linestyle=":", linewidth=1.4, label=f"Fused VAH {fused.vah:,.0f}")
    ax_p.axhline(fused.val, color="#a78bfa", linestyle=":", linewidth=1.4, label=f"Fused VAL {fused.val:,.0f}")
    for i, lvn in enumerate(fused.lvns):
        lbl = "Fused LVN Liquidity Voids" if i == 0 else None
        ax_p.axhline(lvn.price, color="#94a3b8", linestyle="-.", linewidth=0.85, alpha=0.6, label=lbl)

    tf_palette = {"5m": "#93c5fd", "15m": "#38bdf8", "1h": "#34d399", "4h": "#fb923c", "1d": "#e879f9"}
    for tf, prof in profiles.items():
        norm_curve = prof.f_tilde / max(float(np.max(prof.f_tilde)), 1e-12)
        ax_d.plot(norm_curve, prof.grid, color=tf_palette.get(tf, "#cbd5e1"), linewidth=1.2, alpha=0.85, label=tf)

    fused_norm = fused.f_tilde / max(float(np.max(fused.f_tilde)), 1e-12)
    ax_d.fill_betweenx(fused.grid, 0.0, fused_norm, color="#facc15", alpha=0.18)
    ax_d.plot(fused_norm, fused.grid, color="#facc15", linewidth=2.0, label="MTF-Fused")

    ax_p.set_title(
        f"KDE-VPVR-OI Multi-Timeframe Support & Resistance Map | {cfg.spot_symbol} ({source_label})",
        color="#f8fafc",
        fontsize=12,
        fontweight="bold",
        pad=10,
    )
    ax_p.set_ylabel("Price (Quote)", color="#cbd5e1")
    ax_p.set_xlabel(f"Recent {cfg.display_timeframe} Bars (UTC)", color="#cbd5e1")
    ax_d.set_xlabel("Normalized KDE Density", color="#cbd5e1")
    plt.setp(ax_d.get_yticklabels(), visible=False)
    ax_p.legend(loc="upper left", facecolor="#1e293b", edgecolor="#475569", labelcolor="#e2e8f0", fontsize=8)
    ax_d.legend(loc="lower right", facecolor="#1e293b", edgecolor="#475569", labelcolor="#e2e8f0", fontsize=8)

    plt.savefig(cfg.output_image_path, dpi=150, bbox_inches="tight", facecolor="#0f172a")
    plt.close(fig)
    logger.info("Saved S/R chart to %s", cfg.output_image_path)


# ---------------------------------------------------------------------------
# 6. Orchestrator & Invariant Verification
# ---------------------------------------------------------------------------
def run_simulation(cfg: Model1Config) -> Tuple[Dict[str, ProfileResult], List[MTFSRZone], ProfileResult]:
    tf_dfs: Dict[str, pd.DataFrame] = {}
    atr_5m_series: Optional[pd.Series] = None
    source_label = f"CCXT:{cfg.exchange_id}"

    if CCXT_AVAILABLE:
        try:
            fetcher = CCXTMarketDataFetcher(cfg.exchange_id)
            for spec in cfg.timeframes:
                tf_dfs[spec.timeframe] = fetcher.fetch_aligned_timeframe(cfg, spec)
            if cfg.atr_mode == "5m":
                df_5m_live = fetcher.fetch_ohlcv_paginated(cfg.spot_symbol, "5m", 1500)
                atr_5m_series = compute_atr(df_5m_live, cfg.atr_period)
        except Exception as exc:
            if not cfg.synthetic_fallback:
                raise
            logger.warning("CCXT live fetch failed (%s). Falling back to synthetic simulator.", exc)
            tf_dfs = {}

    if not tf_dfs:
        if not cfg.synthetic_fallback:
            raise RuntimeError("CCXT unavailable and synthetic_fallback is disabled.")
        source_label = "Synthetic Multi-Regime 180d Fallback"
        tf_dfs, df_5m_syn = generate_synthetic_mtf_data(cfg)
        if cfg.atr_mode == "5m":
            atr_5m_series = compute_atr(df_5m_syn, cfg.atr_period)

    profiles: Dict[str, ProfileResult] = {}
    for spec in cfg.timeframes:
        df_tf = tf_dfs[spec.timeframe]
        atr_slice = None
        if atr_5m_series is not None:
            atr_slice = atr_5m_series.loc[df_tf.index[0] :]
        prof = compute_kde_vpvr_oi(df_tf, spec.timeframe, cfg, atr_5m_series=atr_slice)
        assert np.isclose(np.sum(prof.weights), 1.0, atol=1e-6), f"{spec.timeframe} weights must sum to 1"
        assert np.isclose(np.sum(prof.f_tilde), 1.0, atol=1e-6), f"{spec.timeframe} PDF must sum to 1"
        assert prof.va_mass >= cfg.gamma_va or prof.n_bars < 5, f"{spec.timeframe} VA mass < gamma"
        assert prof.h >= cfg.grid_floor * prof.delta_g - 1e-9, f"{spec.timeframe} h < floor"
        profiles[spec.timeframe] = prof

    df_disp = tf_dfs[cfg.display_timeframe].tail(cfg.display_candles)
    last_price = float(df_disp["close"].iloc[-1])
    confl_zones = build_confluence_zones(profiles, cfg, last_price)
    fused_profile = fuse_mtf_profiles(profiles, cfg)

    plot_mtf_sr_map(df_disp, profiles, confl_zones, fused_profile, cfg, source_label)

    print(f"\n=== Reference Last Price ({cfg.display_timeframe}): {last_price:,.2f} ===")
    print("=== Ranked Multi-Timeframe S/R Zones (Strategy A) ===")
    for z in confl_zones:
        print(
            f"{z.role:18s} | Center={z.center:9,.1f} | Band=[{z.low:9,.1f}, {z.high:9,.1f}] "
            f"| TFs={','.join(z.timeframes):15s} | Score={z.score:.2f}"
        )
    print(
        f"\n=== Composite MTF Profile (Strategy B) ===\n"
        f"POC={fused_profile.poc:,.1f} | VAL={fused_profile.val:,.1f} | "
        f"VAH={fused_profile.vah:,.1f} | VA Mass={fused_profile.va_mass:.4f} | "
        f"LVNs={[round(l.price, 1) for l in fused_profile.lvns]}"
    )
    return profiles, confl_zones, fused_profile


if __name__ == "__main__":
    config = Model1Config(
        exchange_id="bybit",
        spot_symbol="BTC/USDT",
        perp_symbol="BTC/USDT:USDT",
        atr_mode="tf",
        include_boundary_extrema=True,
    )
    run_simulation(config)