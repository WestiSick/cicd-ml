# Usage guide

This document walks through every scenario the application supports.  
Every action is performed **in the web UI** — no CLI required.

## Table of contents

1. [First run — the `/setup` onboarding screen](#first-run)
2. [Adding your own repository](#scenario-1-add-your-own-repository)
3. [Real-time push → dashboard](#scenario-2-real-time-push--dashboard)
4. [Training a model](#scenario-3-training-a-model)
5. [Comparing scheduling strategies](#scenario-4-comparing-scheduling-strategies)
6. [Refreshing data and retraining](#scenario-5-refreshing-data-and-retraining)
7. [Exporting materials for the thesis](#exporting-materials-for-the-thesis)
8. [Troubleshooting](#troubleshooting)

---

## First run

When the system is launched on an empty database it routes every request to
`/setup` until onboarding completes. The page has four sections:

| # | Section | What it does |
|---|---|---|
| 01 | GitHub Token | Optional. Without it the API limit is 60 req/h. |
| 02 | Seed repositories | Pre-selected public projects with rich CI history. Uncheck any you don't want. |
| 03 | History window | 3 / 6 / 12 months. Longer → more data → better models. |
| 04 | Models to pre-train | Linear, RF, XGBoost (default), LightGBM (default), MLP, LSTM. |

Pressing **Start setup** schedules five background phases:

1. Create GitHub webhooks (if a token was provided).
2. Collect run/job history per repository.
3. Compute feature matrices.
4. Train every selected model in sequence.
5. Run a baseline simulation across all strategies.

Progress streams live on the page. You can close the tab — progress resumes
on return.

## Scenario 1: add your own repository

1. Open `/datasets`.
2. Press **Add repository**.
3. Paste a URL: `https://github.com/<owner>/<repo>`.
4. Select branches (default: just the default branch) and a period.
5. Press **Add**.

The card appears with status `fetching`. The progress bar updates in real
time. When status flips to `synced`, click the card for distributions, top
workflows and a feature-matrix preview.

## Scenario 2: real-time push → dashboard

1. Open `/admin → Webhooks`. The system shows the webhook URL it expects
   GitHub to call. For local development use a tunnel
   (`cloudflared tunnel --url http://localhost:8080`).
2. On any repository card in `/datasets`, toggle **Live webhook** on. If
   your token has the right permissions the system creates the webhook in
   GitHub automatically. Otherwise it shows a `curl` snippet for manual
   setup.
3. Push a commit to that repository.
4. On `/dashboard`, within 1–2 seconds a card appears in the **Live feed**
   with the predicted duration. As GitHub Actions executes, the card
   updates with the actual duration and the prediction error.

## Scenario 3: training a model

1. Open `/experiments → Train new model`.
2. The wizard:
   1. Pick an algorithm: Linear / RF / XGBoost / LightGBM / MLP / LSTM.
   2. Pick a dataset filter: repositories and time range.
   3. Hyperparameters (sensible defaults; optional Optuna search with a
      configurable trial budget).
   4. Time-based train/test split with a cutoff visualised on a timeline.
3. Press **Start training**.
4. A live training page opens with:
   - per-iteration loss / validation MAE / RMSE chart,
   - log tail (last 200 lines),
   - **Cancel** button.
   You may close the tab — the run continues in the background.
5. On completion you see metrics, predicted-vs-actual plot, residuals plot,
   feature importance, and **Activate** / **Compare** actions.

## Scenario 4: comparing scheduling strategies

1. Open `/simulator → New run`.
2. Choose a time window (e.g. `last 7 days`).
3. Check the strategies to compare: FIFO, SJF, EDF, Custom.
4. Press **Run**. Within ~30–60 seconds you see:
   - Mean and p95 wait time per strategy.
   - Makespan.
   - SLA-violation count.
5. Press **Export** to download CSV + PNG ready for the thesis.

## Scenario 5: refreshing data and retraining

1. `/datasets → Refresh all` (or per-card) pulls runs created since the last
   sync.
2. After collection finishes, `/experiments → Retrain active model` queues a
   retraining job on the fresh dataset using the same hyperparameters.

## Exporting materials for the thesis

In the UI:

- `/experiments → Export thesis pack`
- `/simulator → Export thesis pack`

These produce, in `./docs/thesis/`:

- `dataset_stats.md` — dataset characterisation.
- `model_comparison.csv` — metrics for every trained model.
- `strategy_comparison.csv` — strategy comparison.
- `figures/*.pdf` — LaTeX-ready graphics.

## Troubleshooting

Everything is visible in the UI — you should never need to read raw logs.

| Symptom | Where to look | Resolution |
|---|---|---|
| GitHub rate limit hit | `/datasets` card chip shows **Rate limited, retrying in 23 min**. | Wait — collector resumes automatically. Or add a token in `/admin`. |
| Webhook not arriving | `/admin → Webhooks` shows last 50 deliveries with HMAC status. | Re-create webhook from the dataset card; verify the tunnel is up. |
| Model predictions off on a new repo | The model card shows a **coverage %** — how many jobs in this repo resemble the training data. | Add more history via `/datasets` and retrain. |
| A service is unreachable | The header health dot turns yellow/red; `/admin → System health` lists per-service status. | Use **Restart service** there, or check `docker compose logs`. |
