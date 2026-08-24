use std::path::{Path, PathBuf};
use std::str::FromStr;
use std::sync::RwLock;
use std::time::Duration;

use jiff::{Timestamp, civil::Date, tz::TimeZone};
use regex::Regex;
use reqwest::header::{ETAG, IF_NONE_MATCH};
use serde::Deserialize;

const CATALOG_URL: &str =
    "https://raw.githubusercontent.com/pydantic/genai-prices/main/prices/new_data/v2/data.json";
const CATALOG_CACHE_FILE: &str = "ai-price-catalog-v2.json";
const MAX_CATALOG_BYTES: usize = 4 << 20;
const REFRESH_INTERVAL: Duration = Duration::from_secs(60 * 60);

#[derive(Clone, Copy, Default, Eq, PartialEq)]
pub(crate) struct TokenUsage {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub cache_read_tokens: u64,
    pub cache_write_tokens: u64,
    pub reasoning_tokens: u64,
}

pub(crate) struct AiPriceEstimator {
    cache_path: Option<PathBuf>,
    client: reqwest::Client,
    state: RwLock<EstimatorState>,
}

#[derive(Default)]
struct EstimatorState {
    catalog: Option<Catalog>,
    etag: Option<String>,
}

impl AiPriceEstimator {
    pub(crate) fn new(volume: &Path) -> Result<Self, String> {
        Self::build(Some(volume.join(CATALOG_CACHE_FILE)))
    }

    fn build(cache_path: Option<PathBuf>) -> Result<Self, String> {
        let client = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(30))
            .user_agent(concat!("platformd-telemetry/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|error| format!("build AI price catalog client: {error}"))?;
        Ok(Self {
            cache_path,
            client,
            state: RwLock::new(EstimatorState::default()),
        })
    }

    pub(crate) fn estimate(
        &self,
        provider: &str,
        model: &str,
        start_time_unix_nano: u64,
        usage: TokenUsage,
    ) -> Option<f64> {
        if model.trim().is_empty() {
            return None;
        }
        self.state.read().ok()?.catalog.as_ref()?.estimate(
            provider,
            model,
            start_time_unix_nano,
            usage,
        )
    }

    #[cfg(test)]
    pub(crate) fn from_catalog(body: &[u8]) -> Result<Self, String> {
        let estimator = Self::build(None)?;
        estimator
            .state
            .write()
            .map_err(|_| "write AI price catalog state: lock poisoned".to_owned())?
            .catalog = Some(Catalog::parse(body)?);
        Ok(estimator)
    }

    pub(crate) async fn initialize(&self) {
        match self.load_cached().await {
            Ok(loaded) => {
                if loaded {
                    tracing::info!("loaded cached AI price catalog");
                }
            }
            Err(error) => {
                tracing::warn!(%error, "cached AI price catalog is unavailable");
            }
        }
    }

    pub(crate) async fn refresh_forever(&self) {
        loop {
            match self.refresh().await {
                Ok(Some(provider_count)) => {
                    tracing::info!(
                        provider_count,
                        source = CATALOG_URL,
                        "AI price catalog refreshed"
                    );
                }
                Ok(None) => {}
                Err(error) => {
                    tracing::warn!(
                        %error,
                        source = CATALOG_URL,
                        "AI price catalog refresh failed; retaining the current catalog"
                    );
                }
            }
            tokio::time::sleep(REFRESH_INTERVAL).await;
        }
    }

    async fn refresh(&self) -> Result<Option<usize>, String> {
        let etag = self
            .state
            .read()
            .map_err(|_| "read AI price catalog state: lock poisoned".to_owned())?
            .etag
            .clone();
        let mut request = self.client.get(CATALOG_URL);
        if let Some(etag) = etag {
            request = request.header(IF_NONE_MATCH, etag);
        }
        let mut response = request
            .send()
            .await
            .map_err(|error| format!("download AI price catalog: {error}"))?;
        if response.status() == reqwest::StatusCode::NOT_MODIFIED {
            return Ok(None);
        }
        response = response
            .error_for_status()
            .map_err(|error| format!("download AI price catalog: {error}"))?;
        if response
            .content_length()
            .is_some_and(|length| length > MAX_CATALOG_BYTES as u64)
        {
            return Err("download AI price catalog: response exceeds size limit".into());
        }
        let response_etag = response
            .headers()
            .get(ETAG)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        let mut body = Vec::new();
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|error| format!("read AI price catalog: {error}"))?
        {
            let next_length = body
                .len()
                .checked_add(chunk.len())
                .ok_or_else(|| "read AI price catalog: response size overflow".to_owned())?;
            if next_length > MAX_CATALOG_BYTES {
                return Err("read AI price catalog: response exceeds size limit".into());
            }
            body.extend_from_slice(&chunk);
        }
        let (body, catalog) = tokio::task::spawn_blocking(move || {
            Catalog::parse(&body).map(|catalog| (body, catalog))
        })
        .await
        .map_err(|error| format!("join AI price catalog parser: {error}"))??;
        let provider_count = catalog.providers.len();
        {
            let mut state = self
                .state
                .write()
                .map_err(|_| "write AI price catalog state: lock poisoned".to_owned())?;
            state.catalog = Some(catalog);
            state.etag = response_etag;
        }
        if let Err(error) = self.persist(&body).await {
            tracing::warn!(%error, "persist AI price catalog cache failed");
        }
        Ok(Some(provider_count))
    }

    async fn load_cached(&self) -> Result<bool, String> {
        let Some(cache_path) = self.cache_path.as_ref() else {
            return Ok(false);
        };
        let body = match tokio::fs::read(cache_path).await {
            Ok(body) => body,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(false),
            Err(error) => return Err(format!("read AI price catalog cache: {error}")),
        };
        if body.len() > MAX_CATALOG_BYTES {
            return Err("read AI price catalog cache: file exceeds size limit".into());
        }
        let catalog = tokio::task::spawn_blocking(move || Catalog::parse(&body))
            .await
            .map_err(|error| format!("join cached AI price catalog parser: {error}"))??;
        self.state
            .write()
            .map_err(|_| "write AI price catalog state: lock poisoned".to_owned())?
            .catalog = Some(catalog);
        Ok(true)
    }

    async fn persist(&self, body: &[u8]) -> Result<(), String> {
        let Some(cache_path) = self.cache_path.as_ref() else {
            return Ok(());
        };
        let temporary_path = cache_path.with_extension("json.tmp");
        tokio::fs::write(&temporary_path, body)
            .await
            .map_err(|error| format!("write AI price catalog cache: {error}"))?;
        tokio::fs::rename(&temporary_path, cache_path)
            .await
            .map_err(|error| format!("replace AI price catalog cache: {error}"))
    }
}

struct Catalog {
    providers: Vec<Provider>,
}

impl Catalog {
    fn parse(body: &[u8]) -> Result<Self, String> {
        let raw = serde_json::from_slice::<Vec<RawProvider>>(body)
            .map_err(|error| format!("decode AI price catalog: {error}"))?;
        if raw.is_empty() {
            return Err("decode AI price catalog: catalog is empty".into());
        }
        let providers = raw
            .into_iter()
            .map(Provider::try_from)
            .collect::<Result<Vec<_>, _>>()?;
        Ok(Self { providers })
    }

    fn estimate(
        &self,
        provider_id: &str,
        model_id: &str,
        start_time_unix_nano: u64,
        usage: TokenUsage,
    ) -> Option<f64> {
        let mut model_id = model_id.trim().to_owned();
        let mut provider_id = provider_id.trim().to_ascii_lowercase();
        if provider_id == "litellm"
            && let Some((candidate_provider, candidate_model)) = model_id.split_once('/')
            && self.provider(candidate_provider).is_some()
        {
            provider_id = candidate_provider.to_ascii_lowercase();
            model_id = candidate_model.to_owned();
        }
        let instant = PricingInstant::from_unix_nanos(start_time_unix_nano)?;
        if !provider_id.is_empty() {
            if let Some(provider) = self.provider(&provider_id) {
                return self.estimate_for_provider(provider, &model_id, instant, usage);
            }
            if provider_id != "litellm" {
                return None;
            }
        }
        let provider = self.providers.iter().find(|provider| {
            provider
                .model_match
                .as_ref()
                .is_some_and(|logic| logic.matches(&model_id))
        })?;
        self.estimate_for_provider(provider, &model_id, instant, usage)
    }

    fn provider(&self, provider_id: &str) -> Option<&Provider> {
        let normalized_provider_id = provider_id.trim().to_ascii_lowercase();
        if let Some(provider) = self
            .providers
            .iter()
            .find(|provider| provider.id == normalized_provider_id)
        {
            return Some(provider);
        }
        let canonical_provider_id = canonical_provider_id(&normalized_provider_id);
        if canonical_provider_id != normalized_provider_id
            && let Some(provider) = self
                .providers
                .iter()
                .find(|provider| provider.id == canonical_provider_id)
        {
            return Some(provider);
        }
        self.providers.iter().find(|provider| {
            provider
                .provider_match
                .as_ref()
                .is_some_and(|logic| logic.matches(&normalized_provider_id))
        })
    }

    fn estimate_for_provider(
        &self,
        provider: &Provider,
        model_id: &str,
        instant: PricingInstant,
        usage: TokenUsage,
    ) -> Option<f64> {
        let model = provider
            .models
            .iter()
            .find(|model| model.matcher.matches(model_id))
            .or_else(|| {
                provider
                    .fallback_model_providers
                    .iter()
                    .find_map(|provider_id| {
                        self.providers
                            .iter()
                            .find(|candidate| candidate.id == *provider_id)?
                            .models
                            .iter()
                            .find(|model| model.matcher.matches(model_id))
                    })
            })?;
        model.active_prices(instant).estimate(usage)
    }
}

struct Provider {
    id: String,
    provider_match: Option<MatchLogic>,
    model_match: Option<MatchLogic>,
    fallback_model_providers: Vec<String>,
    models: Vec<Model>,
}

impl TryFrom<RawProvider> for Provider {
    type Error = String;

    fn try_from(raw: RawProvider) -> Result<Self, Self::Error> {
        Ok(Self {
            id: raw.id.to_ascii_lowercase(),
            provider_match: raw.provider_match.map(MatchLogic::try_from).transpose()?,
            model_match: raw.model_match.map(MatchLogic::try_from).transpose()?,
            fallback_model_providers: raw
                .fallback_model_providers
                .into_iter()
                .map(|provider| provider.to_ascii_lowercase())
                .collect(),
            models: raw
                .models
                .into_iter()
                .map(Model::try_from)
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

struct Model {
    matcher: MatchLogic,
    prices: ModelPrices,
}

impl Model {
    fn active_prices(&self, instant: PricingInstant) -> &PriceSet {
        match &self.prices {
            ModelPrices::Direct(prices) => prices,
            ModelPrices::Conditional(prices) => prices
                .iter()
                .rev()
                .find(|price| {
                    price
                        .constraint
                        .as_ref()
                        .is_none_or(|constraint| constraint.matches(instant))
                })
                .unwrap_or(&prices[0])
                .prices
                .as_ref(),
        }
    }
}

impl TryFrom<RawModel> for Model {
    type Error = String;

    fn try_from(raw: RawModel) -> Result<Self, Self::Error> {
        let prices = match raw.prices {
            RawModelPrices::Direct(prices) => ModelPrices::Direct(PriceSet::try_from(prices)?),
            RawModelPrices::Conditional(prices) => {
                if prices.is_empty() {
                    return Err(format!("model {} has no conditional prices", raw.id));
                }
                ModelPrices::Conditional(
                    prices
                        .into_iter()
                        .map(ConditionalPrice::try_from)
                        .collect::<Result<Vec<_>, _>>()?,
                )
            }
        };
        Ok(Self {
            matcher: MatchLogic::try_from(raw.matcher)?,
            prices,
        })
    }
}

enum ModelPrices {
    Direct(PriceSet),
    Conditional(Vec<ConditionalPrice>),
}

struct ConditionalPrice {
    constraint: Option<Constraint>,
    prices: Box<PriceSet>,
}

impl TryFrom<RawConditionalPrice> for ConditionalPrice {
    type Error = String;

    fn try_from(raw: RawConditionalPrice) -> Result<Self, Self::Error> {
        Ok(Self {
            constraint: raw.constraint.map(Constraint::try_from).transpose()?,
            prices: Box::new(PriceSet::try_from(raw.prices)?),
        })
    }
}

enum Constraint {
    StartDate(Date),
    TimeOfDay { start: u32, end: u32 },
}

impl Constraint {
    fn matches(&self, instant: PricingInstant) -> bool {
        match *self {
            Self::StartDate(date) => instant.date >= date,
            Self::TimeOfDay { start, end } if end < start => {
                instant.second_of_day >= start || instant.second_of_day < end
            }
            Self::TimeOfDay { start, end } => {
                instant.second_of_day >= start && instant.second_of_day < end
            }
        }
    }
}

impl TryFrom<RawConstraint> for Constraint {
    type Error = String;

    fn try_from(raw: RawConstraint) -> Result<Self, Self::Error> {
        match raw {
            RawConstraint::StartDate { start_date } => Date::from_str(&start_date)
                .map(Self::StartDate)
                .map_err(|error| format!("invalid AI price start date {start_date}: {error}")),
            RawConstraint::TimeOfDay {
                start_time,
                end_time,
            } => Ok(Self::TimeOfDay {
                start: parse_utc_time(&start_time)?,
                end: parse_utc_time(&end_time)?,
            }),
        }
    }
}

#[derive(Clone, Copy)]
struct PricingInstant {
    date: Date,
    second_of_day: u32,
}

impl PricingInstant {
    fn from_unix_nanos(value: u64) -> Option<Self> {
        let zoned = Timestamp::from_nanosecond(i128::from(value))
            .ok()?
            .to_zoned(TimeZone::UTC);
        let second_of_day = u32::try_from(zoned.hour()).ok()? * 3_600
            + u32::try_from(zoned.minute()).ok()? * 60
            + u32::try_from(zoned.second()).ok()?;
        Some(Self {
            date: zoned.date(),
            second_of_day,
        })
    }
}

fn parse_utc_time(value: &str) -> Result<u32, String> {
    let value = value.strip_suffix('Z').unwrap_or(value);
    let mut parts = value.split(':');
    let hour = parts
        .next()
        .and_then(|value| value.parse::<u32>().ok())
        .filter(|value| *value < 24);
    let minute = parts
        .next()
        .and_then(|value| value.parse::<u32>().ok())
        .filter(|value| *value < 60);
    let second = parts
        .next()
        .and_then(|value| value.parse::<u32>().ok())
        .filter(|value| *value < 60);
    if parts.next().is_some() || hour.is_none() || minute.is_none() || second.is_none() {
        return Err(format!("invalid AI price UTC time {value}"));
    }
    Ok(hour.expect("hour was checked") * 3_600
        + minute.expect("minute was checked") * 60
        + second.expect("second was checked"))
}

fn canonical_provider_id(provider_id: &str) -> &str {
    match provider_id {
        "azure.ai.openai" | "azure.ai.inference" => "azure",
        "aws.bedrock" => "aws",
        "gcp.gemini" | "gcp.vertex_ai" => "google",
        "mistral_ai" => "mistral",
        "x_ai" => "x-ai",
        _ if provider_id == "azure"
            || provider_id.starts_with("azure.")
            || provider_id.starts_with("azure-") =>
        {
            "azure"
        }
        _ if provider_id == "xai"
            || provider_id.starts_with("xai.")
            || provider_id.starts_with("xai-") =>
        {
            "x-ai"
        }
        _ => provider_id,
    }
}

enum MatchLogic {
    Or(Vec<Self>),
    And(Vec<Self>),
    Equals(String),
    StartsWith(String),
    EndsWith(String),
    Contains(String),
    Regex(Regex),
}

impl MatchLogic {
    fn matches(&self, value: &str) -> bool {
        match self {
            Self::Or(clauses) => clauses.iter().any(|clause| clause.matches(value)),
            Self::And(clauses) => clauses.iter().all(|clause| clause.matches(value)),
            Self::Equals(expected) => value.eq_ignore_ascii_case(expected),
            Self::StartsWith(expected) => value.to_ascii_lowercase().starts_with(expected),
            Self::EndsWith(expected) => value.to_ascii_lowercase().ends_with(expected),
            Self::Contains(expected) => value.to_ascii_lowercase().contains(expected),
            Self::Regex(pattern) => pattern.is_match(value),
        }
    }
}

impl TryFrom<RawMatchLogic> for MatchLogic {
    type Error = String;

    fn try_from(raw: RawMatchLogic) -> Result<Self, Self::Error> {
        match raw {
            RawMatchLogic::Or { or } => Ok(Self::Or(
                or.into_iter()
                    .map(Self::try_from)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            RawMatchLogic::And { and } => Ok(Self::And(
                and.into_iter()
                    .map(Self::try_from)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            RawMatchLogic::Equals { equals } => Ok(Self::Equals(equals.to_ascii_lowercase())),
            RawMatchLogic::StartsWith { starts_with } => {
                Ok(Self::StartsWith(starts_with.to_ascii_lowercase()))
            }
            RawMatchLogic::EndsWith { ends_with } => {
                Ok(Self::EndsWith(ends_with.to_ascii_lowercase()))
            }
            RawMatchLogic::Contains { contains } => {
                Ok(Self::Contains(contains.to_ascii_lowercase()))
            }
            RawMatchLogic::Regex { regex } => Regex::new(&regex)
                .map(Self::Regex)
                .map_err(|error| format!("invalid AI price match regex {regex}: {error}")),
        }
    }
}

#[derive(Clone, Default, Deserialize)]
struct PriceSet {
    input_mtok: Option<Rate>,
    output_mtok: Option<Rate>,
    cache_read_mtok: Option<Rate>,
    cache_write_mtok: Option<Rate>,
    output_reasoning_mtok: Option<Rate>,
    requests_kcount: Option<Rate>,
    #[serde(skip)]
    known_free: bool,
}

impl TryFrom<RawPriceSet> for PriceSet {
    type Error = String;

    fn try_from(raw: RawPriceSet) -> Result<Self, Self::Error> {
        let known_free = raw.0.is_empty();
        let mut prices = serde_json::from_value::<Self>(serde_json::Value::Object(raw.0))
            .map_err(|error| format!("decode AI price set: {error}"))?;
        prices.known_free = known_free;
        prices.validate()
    }
}

impl PriceSet {
    fn validate(mut self) -> Result<Self, String> {
        for rate in [
            self.input_mtok.as_mut(),
            self.output_mtok.as_mut(),
            self.cache_read_mtok.as_mut(),
            self.cache_write_mtok.as_mut(),
            self.output_reasoning_mtok.as_mut(),
            self.requests_kcount.as_mut(),
        ]
        .into_iter()
        .flatten()
        {
            rate.validate()?;
        }
        Ok(self)
    }

    fn estimate(&self, usage: TokenUsage) -> Option<f64> {
        if self.known_free {
            return Some(0.0);
        }
        let mut input_tokens = usage.input_tokens;
        if self.cache_read_mtok.is_some() {
            input_tokens = input_tokens.checked_sub(usage.cache_read_tokens)?;
        }
        if self.cache_write_mtok.is_some() {
            input_tokens = input_tokens.checked_sub(usage.cache_write_tokens)?;
        }
        let output_tokens = if self.output_reasoning_mtok.is_some() {
            usage.output_tokens.checked_sub(usage.reasoning_tokens)?
        } else {
            usage.output_tokens
        };
        let total_input_tokens = usage.input_tokens;
        let has_priced_usage = (input_tokens > 0 && self.input_mtok.is_some())
            || (output_tokens > 0 && self.output_mtok.is_some())
            || (usage.cache_read_tokens > 0 && self.cache_read_mtok.is_some())
            || (usage.cache_write_tokens > 0 && self.cache_write_mtok.is_some())
            || (usage.reasoning_tokens > 0 && self.output_reasoning_mtok.is_some())
            || self.requests_kcount.is_some();
        if !has_priced_usage {
            return None;
        }
        let total = self.input_mtok.as_ref().map_or(0.0, |rate| {
            rate.cost(input_tokens, total_input_tokens, 1_000_000)
        }) + self.output_mtok.as_ref().map_or(0.0, |rate| {
            rate.cost(output_tokens, total_input_tokens, 1_000_000)
        }) + self.cache_read_mtok.as_ref().map_or(0.0, |rate| {
            rate.cost(usage.cache_read_tokens, total_input_tokens, 1_000_000)
        }) + self.cache_write_mtok.as_ref().map_or(0.0, |rate| {
            rate.cost(usage.cache_write_tokens, total_input_tokens, 1_000_000)
        }) + self.output_reasoning_mtok.as_ref().map_or(0.0, |rate| {
            rate.cost(usage.reasoning_tokens, total_input_tokens, 1_000_000)
        }) + self
            .requests_kcount
            .as_ref()
            .map_or(0.0, |rate| rate.cost(1, total_input_tokens, 1_000));
        total.is_finite().then_some(total)
    }
}

#[derive(Clone, Deserialize)]
#[serde(untagged)]
enum Rate {
    Scalar(f64),
    Tiered { base: f64, tiers: Vec<Tier> },
}

impl Rate {
    fn validate(&mut self) -> Result<(), String> {
        if let Self::Tiered { tiers, .. } = self {
            tiers.sort_by_key(|tier| tier.start);
        }
        let valid = match self {
            Self::Scalar(value) => value.is_finite() && *value >= 0.0,
            Self::Tiered { base, tiers } => {
                base.is_finite()
                    && *base >= 0.0
                    && tiers
                        .iter()
                        .all(|tier| tier.price.is_finite() && tier.price >= 0.0)
            }
        };
        valid
            .then_some(())
            .ok_or_else(|| "AI price catalog contains an invalid price".into())
    }

    fn cost(&self, count: u64, total_input_tokens: u64, per: u64) -> f64 {
        let price = match self {
            Self::Scalar(price) => *price,
            Self::Tiered { base, tiers } => tiers.iter().fold(*base, |price, tier| {
                if total_input_tokens > tier.start {
                    tier.price
                } else {
                    price
                }
            }),
        };
        price * count as f64 / per as f64
    }
}

#[derive(Clone, Deserialize)]
struct Tier {
    start: u64,
    price: f64,
}

#[derive(Deserialize)]
struct RawProvider {
    id: String,
    #[serde(default)]
    provider_match: Option<RawMatchLogic>,
    #[serde(default)]
    model_match: Option<RawMatchLogic>,
    #[serde(default)]
    fallback_model_providers: Vec<String>,
    models: Vec<RawModel>,
}

#[derive(Deserialize)]
struct RawModel {
    id: String,
    #[serde(rename = "match")]
    matcher: RawMatchLogic,
    prices: RawModelPrices,
}

#[derive(Deserialize)]
#[serde(untagged)]
enum RawModelPrices {
    Direct(RawPriceSet),
    Conditional(Vec<RawConditionalPrice>),
}

#[derive(Deserialize)]
#[serde(transparent)]
struct RawPriceSet(serde_json::Map<String, serde_json::Value>);

#[derive(Deserialize)]
struct RawConditionalPrice {
    #[serde(default)]
    constraint: Option<RawConstraint>,
    prices: RawPriceSet,
}

#[derive(Deserialize)]
#[serde(untagged)]
enum RawConstraint {
    StartDate {
        start_date: String,
    },
    TimeOfDay {
        start_time: String,
        end_time: String,
    },
}

#[derive(Deserialize)]
#[serde(untagged)]
enum RawMatchLogic {
    Or { or: Vec<Self> },
    And { and: Vec<Self> },
    Equals { equals: String },
    StartsWith { starts_with: String },
    EndsWith { ends_with: String },
    Contains { contains: String },
    Regex { regex: String },
}

#[cfg(test)]
mod tests {
    use super::*;

    fn nanos(timestamp: &str) -> u64 {
        Timestamp::from_str(timestamp)
            .unwrap()
            .as_nanosecond()
            .try_into()
            .unwrap()
    }

    fn catalog() -> Catalog {
        Catalog::parse(
            br#"[{
              "id":"openai",
              "name":"OpenAI",
              "api_pattern":"",
              "provider_match":{"or":[{"equals":"openai.responses"},{"equals":"azure_openai"}]},
              "model_match":{"starts_with":"gpt-"},
              "models":[{
                "id":"gpt-test",
                "match":{"starts_with":"gpt-test"},
                "prices":[
                  {"prices":{"input_mtok":2,"cache_read_mtok":0.5,"output_mtok":4}},
                  {"constraint":{"start_date":"2026-07-30"},"prices":{"input_mtok":1,"cache_read_mtok":0.25,"output_mtok":3}}
                ]
              },{
                "id":"gpt-tiered",
                "match":{"equals":"gpt-tiered"},
                "prices":{"input_mtok":{"base":1,"tiers":[{"start":100,"price":2}]},"output_mtok":5,"output_reasoning_mtok":8}
              }]
            },{
              "id":"deepseek",
              "name":"DeepSeek",
              "api_pattern":"",
              "models":[{
                "id":"deepseek-chat",
                "match":{"equals":"deepseek-chat"},
                "prices":[
                  {"prices":{"input_mtok":0.1,"output_mtok":0.2}},
                  {"constraint":{"start_time":"00:30:00Z","end_time":"16:30:00Z"},"prices":{"input_mtok":0.3,"output_mtok":0.6}}
                ]
              }]
            }]"#,
        )
        .unwrap()
    }

    fn assert_cost(actual: Option<f64>, expected: f64) {
        let actual = actual.expect("catalog should match provider and model");
        assert!((actual - expected).abs() < 1e-12, "{actual} != {expected}");
    }

    #[tokio::test]
    async fn loads_the_last_known_good_catalog_from_the_volume() {
        let volume = tempfile::tempdir().unwrap();
        std::fs::write(
            volume.path().join(CATALOG_CACHE_FILE),
            br#"[{"id":"openai","models":[{"id":"gpt-cached","match":{"equals":"gpt-cached"},"prices":{"input_mtok":2}}]}]"#,
        )
        .unwrap();
        let estimator = AiPriceEstimator::new(volume.path()).unwrap();

        assert!(estimator.load_cached().await.unwrap());
        assert_cost(
            estimator.estimate(
                "openai",
                "gpt-cached",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage {
                    input_tokens: 1_000_000,
                    ..TokenUsage::default()
                },
            ),
            2.0,
        );
    }

    #[test]
    fn estimates_historical_and_cache_aware_cost_per_generation() {
        let usage = TokenUsage {
            input_tokens: 1_000_000,
            output_tokens: 100_000,
            cache_read_tokens: 200_000,
            ..TokenUsage::default()
        };
        let catalog = catalog();
        assert_cost(
            catalog.estimate(
                "openai.responses",
                "gpt-test-2026-01-01",
                nanos("2026-07-29T23:00:00Z"),
                usage,
            ),
            2.1,
        );
        assert_cost(
            catalog.estimate(
                "azure_openai",
                "gpt-test",
                nanos("2026-07-30T01:00:00Z"),
                usage,
            ),
            1.15,
        );
    }

    #[test]
    fn applies_daily_and_tiered_prices_to_individual_usage() {
        let catalog = catalog();
        let usage = TokenUsage {
            input_tokens: 1_000_000,
            output_tokens: 1_000_000,
            ..TokenUsage::default()
        };
        assert_cost(
            catalog.estimate(
                "deepseek",
                "deepseek-chat",
                nanos("2026-08-01T01:00:00Z"),
                usage,
            ),
            0.9,
        );
        assert_cost(
            catalog.estimate(
                "deepseek",
                "deepseek-chat",
                nanos("2026-08-01T20:00:00Z"),
                usage,
            ),
            0.3,
        );
        assert_cost(
            catalog.estimate(
                "openai",
                "gpt-tiered",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage {
                    input_tokens: 101,
                    output_tokens: 20,
                    reasoning_tokens: 5,
                    ..TokenUsage::default()
                },
            ),
            (101.0 * 2.0 + 15.0 * 5.0 + 5.0 * 8.0) / 1_000_000.0,
        );
    }

    #[test]
    fn rejects_inconsistent_token_breakdowns() {
        assert_eq!(
            catalog().estimate(
                "openai",
                "gpt-test",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage {
                    input_tokens: 10,
                    cache_read_tokens: 11,
                    ..TokenUsage::default()
                },
            ),
            None
        );
    }

    #[test]
    fn respects_explicit_provider_and_request_only_pricing() {
        assert_eq!(
            catalog().estimate(
                "unknown-provider",
                "gpt-test",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage {
                    input_tokens: 100,
                    ..TokenUsage::default()
                },
            ),
            None
        );
        let request_catalog = Catalog::parse(
            br#"[{"id":"request-provider","models":[{"id":"request-model","match":{"equals":"request-model"},"prices":{"requests_kcount":0.5}}]}]"#,
        )
        .unwrap();
        assert_cost(
            request_catalog.estimate(
                "request-provider",
                "request-model",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage::default(),
            ),
            0.0005,
        );
    }

    #[test]
    fn provider_resolution_prefers_exact_ids_over_broad_matchers() {
        let catalog = Catalog::parse(
            br#"[{
              "id":"cerebras",
              "provider_match":{"contains":"cerebras"},
              "models":[{"id":"shared","match":{"equals":"shared"},"prices":{"input_mtok":9}}]
            },{
              "id":"huggingface_cerebras",
              "models":[{"id":"shared","match":{"equals":"shared"},"prices":{"input_mtok":2}}]
            }]"#,
        )
        .unwrap();

        assert_cost(
            catalog.estimate(
                "huggingface_cerebras",
                "shared",
                nanos("2026-08-01T20:00:00Z"),
                TokenUsage {
                    input_tokens: 1_000_000,
                    ..TokenUsage::default()
                },
            ),
            2.0,
        );
    }

    #[test]
    fn normalizes_standard_ai_sdk_provider_names() {
        let catalog = Catalog::parse(
            br#"[{
              "id":"openai",
              "provider_match":{"contains":"openai"},
              "models":[{"id":"shared","match":{"equals":"shared"},"prices":{"input_mtok":9}}]
            },{
              "id":"azure",
              "models":[{"id":"shared","match":{"equals":"shared"},"prices":{"input_mtok":1}}]
            },{
              "id":"x-ai",
              "models":[{"id":"grok-test","match":{"equals":"grok-test"},"prices":{"input_mtok":2}}]
            },{
              "id":"google",
              "models":[{"id":"gemini-test","match":{"equals":"gemini-test"},"prices":{"input_mtok":3}}]
            },{
              "id":"aws",
              "models":[{"id":"claude-test","match":{"equals":"claude-test"},"prices":{"input_mtok":4}}]
            },{
              "id":"mistral",
              "models":[{"id":"mistral-test","match":{"equals":"mistral-test"},"prices":{"input_mtok":5}}]
            }]"#,
        )
        .unwrap();
        let usage = TokenUsage {
            input_tokens: 1_000_000,
            ..TokenUsage::default()
        };
        let timestamp = nanos("2026-08-01T20:00:00Z");

        for (provider, model, expected) in [
            ("azure.ai.openai", "shared", 1.0),
            ("azure.ai.inference", "shared", 1.0),
            ("azure-openai.chat", "shared", 1.0),
            ("x_ai", "grok-test", 2.0),
            ("xai.chat", "grok-test", 2.0),
            ("gcp.gemini", "gemini-test", 3.0),
            ("gcp.vertex_ai", "gemini-test", 3.0),
            ("aws.bedrock", "claude-test", 4.0),
            ("mistral_ai", "mistral-test", 5.0),
        ] {
            assert_cost(
                catalog.estimate(provider, model, timestamp, usage),
                expected,
            );
        }
    }

    #[test]
    fn distinguishes_known_free_models_from_unsupported_price_units() {
        let catalog = Catalog::parse(
            br#"[{
              "id":"openrouter",
              "models":[{
                "id":"model:free",
                "match":{"equals":"model:free"},
                "prices":{}
              },{
                "id":"audio-only",
                "match":{"equals":"audio-only"},
                "prices":{"input_audio_mtok":1}
              }]
            }]"#,
        )
        .unwrap();
        let usage = TokenUsage {
            input_tokens: 100,
            ..TokenUsage::default()
        };
        let timestamp = nanos("2026-08-01T20:00:00Z");

        assert_eq!(
            catalog.estimate("openrouter", "model:free", timestamp, usage),
            Some(0.0)
        );
        assert_eq!(
            catalog.estimate("openrouter", "audio-only", timestamp, usage),
            None
        );
    }
}
