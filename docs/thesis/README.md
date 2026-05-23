# `docs/thesis/` — материалы для диссертации

Все артефакты, которые попадают в финальный текст работы. Папка
структурирована так, чтобы LaTeX-исходник мог ссылаться на файлы
напрямую без копирования.

## Структура

```
docs/thesis/
├── figures/        # PNG-фигуры (из EDA notebook + thesis-pack)
├── screenshots/    # Скриншоты UI для глав 4–5
├── *.csv           # Таблицы метрик (от /api/experiments/export-thesis-pack)
└── *.md            # Технические записки (например, dataset_stats.md)
```

## Как генерируется

| Артефакт | Команда | Что появится |
|---|---|---|
| EDA-фигуры (Глава 3) | `make eda-figures` | `figures/fig_3_1_*.png ... fig_3_5_*.png` |
| Метрики моделей + симуляций | `make thesis-pack` или кнопка «Export thesis pack» в /experiments | `model_comparison.csv`, `strategy_comparison.csv`, `figures/*.pdf` |
| Скриншоты UI | вручную (см. ниже) | `screenshots/*.png` |

## Скриншоты — процедура

План явно требует **тёмную тему, разрешение 1440×900, без курсора, с реальными данными**. Алгоритм:

1. Включить тёмную тему (переключатель в шапке).
2. Выставить размер окна браузера 1440×900 (Chrome DevTools → Device Mode → Responsive → 1440 × 900).
3. Скрыть курсор (можно использовать `Cmd+Shift+P` → «Hide cursor» в Chrome).
4. Заскринить страницы:
   - `/dashboard` — после нескольких webhook-событий, чтобы лента не была пустой.
   - `/datasets` — после bootstrap, с заполненными карточками.
   - `/datasets/{id}` — для самого крупного репозитория (vitejs/vite или prometheus).
   - `/experiments` — с обученными моделями.
   - `/experiments/jobs/{id}` — обучение XGBoost после завершения.
   - `/experiments/compare?ids=...` — сравнение всех 5–6 моделей.
   - `/simulator` — после прогона на 7-дневном окне.
   - `/admin` — Settings + Activity log.
5. Сохранить как `screenshots/screenshot_NN_<page>_<purpose>.png`.

Используем PNG, не JPEG — текст должен быть резким для печати.

## Подключение в LaTeX

```latex
\begin{figure}[h]
  \centering
  \includegraphics[width=0.85\textwidth]{figures/fig_3_1_duration_distribution.png}
  \caption{Распределение длительностей CI-задач (логарифмическая шкала).}
  \label{fig:duration_dist}
\end{figure}
```

CSV-таблицы в Главу 4 удобно вставлять через `pgfplotstable`:

```latex
\pgfplotstabletypeset[col sep=comma, columns/algo/.style={string type}]{model_comparison.csv}
```

## Воспроизводимость

Один из критериев успеха диссертации — **«каждая фигура воспроизводится из текущей БД одной командой»**. Проверочный сценарий перед сдачей:

```bash
make snapshot                # сохранили текущую БД
rm -rf docs/thesis/figures   # стёрли фигуры
make eda-figures             # сгенерировали заново
make thesis-pack             # экспортировали метрики
git status docs/thesis/      # должны появиться все ожидаемые файлы
```

## Не коммитить

- `*.aux`, `*.log`, `*.toc` — артефакты LaTeX-сборки.
- Размер фигур > 5 МБ — это лишний баласт в репозитории; такие сжимать `pngcrush` или `pngquant`.
