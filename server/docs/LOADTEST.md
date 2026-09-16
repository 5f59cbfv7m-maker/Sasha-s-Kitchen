# Как воспроизвести замеры производительности

Числа в `PERFORMANCE.md` получены этим набором. Повторяйте его после
любой правки индексов или построителя запросов.

**Важно:** интеграционные тесты делают `TRUNCATE`, поэтому нагрузочный
набор после `go test` надо генерировать заново.

## 1. Поднять базу

```bash
createdb sk_store
export DATABASE_URL="postgres://sk:sk@127.0.0.1:5432/sk_store?sslmode=disable"
go run ./cmd/migrate
```

## 2. Сгенерировать данные

Скрипт создаёт 1 000 авторов, 50 000 опубликованных рецептов и около
250 000 ингредиентов из словаря в 300 наименований.

```bash
psql "$DATABASE_URL" -f docs/loadtest.sql
```

Триггер производных колонок на время загрузки отключается: иначе он
пересчитывал бы рецепт заново на каждом ингредиенте. Один проход в конце
даёт тот же результат за долю работы — так же стоит поступать при любом
массовом импорте каталога.

## 3. Посмотреть планы

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT r.id FROM recipes r JOIN users u ON u.id = r.author_id
 WHERE r.status='published' AND r.deleted_at IS NULL AND u.status='active'
 ORDER BY r.published_at DESC, r.id DESC LIMIT 21;
```

На что смотреть:

- **`Rows Removed by Filter`** — главный индикатор. Большое число означает,
  что планировщик идёт по индексу сортировки и отбрасывает лишнее; при
  селективном фильтре это может увести его далеко.
- **`Seq Scan on recipes`** — почти всегда ошибка на этой таблице.
- **`Rows Removed by Index Recheck`** — признак, что GIN отдаёт слишком
  широкий набор кандидатов.

## 4. Самый тяжёлый запрос

«Могу приготовить сейчас» — его и надо мерить в первую очередь:

```sql
EXPLAIN (ANALYZE, BUFFERS)
WITH pantry AS (SELECT array_agg('продукт ' || g) AS keys FROM generate_series(1,60) g)
SELECT r.id
  FROM recipes r
  JOIN (
      SELECT ri.recipe_id, count(*) AS matched
        FROM recipe_ingredients ri, pantry p
       WHERE NOT ri.is_optional AND ri.product_key = ANY(p.keys)
       GROUP BY ri.recipe_id
  ) m ON m.recipe_id = r.id AND m.matched = r.required_count
  JOIN users u ON u.id = r.author_id
 WHERE r.status='published' AND r.deleted_at IS NULL AND u.status='active'
 ORDER BY r.published_at DESC, r.id DESC LIMIT 21;
```

Ориентир: **около 50 мс** на 50k рецептов, все шаги index-only. Если
появился `Seq Scan on recipes` — проверьте, что жив
`recipes_cancook_probe_idx`, и что после загрузки данных выполнен
`ANALYZE`.

## 5. Не забыть ANALYZE

```sql
ANALYZE recipes; ANALYZE recipe_ingredients;
```

Без свежей статистики планировщик выбирает мимо, и все замеры врут.

## Чего здесь нет

Это замеры **одиночных** запросов. Конкурентной нагрузки, насыщения пула
соединений и поведения под сотнями параллельных клиентов здесь нет — это
следующий шаг, и делать его надо на настоящем железе, а не в контейнере
разработки.
