# Irrigacao-MSS

Microsserviço de recomendação de irrigação desenvolvido em Go. O serviço integra leituras de sensores, coordenadas de dispositivos armazenadas no Supabase e previsão meteorológica da Open-Meteo. Toda a lógica de filtragem, projeção, classificação, diagnósticos e recomendação é calculada no backend; os clientes (por exemplo, o aplicativo Expo) devem apenas consumir e apresentar os campos retornados pela API.

> **Importante:** o microsserviço oferece apoio à decisão, não comanda bombas, válvulas ou outros atuadores. A escala do sensor e os parâmetros devem ser calibrados com dados de campo antes de usar as recomendações operacionalmente.

## 1. Responsabilidades do MSS

O microsserviço é a fonte de verdade para:

- validação, filtragem e ordenação temporal das leituras;
- estimativa de nível e tendência da umidade e projeção futura;
- classificação da tendência e avaliação da incerteza da projeção;
- detecção de sequências persistentes próximas aos limites inferior e superior do sensor;
- resumo dos dados meteorológicos;
- classificação do solo e recomendação de irrigação;
- mensagens explicativas dos diagnósticos e da recomendação.

O frontend não deve recalcular esses critérios. Ele pode formatar datas e unidades, desenhar gráficos, traduzir códigos para rótulos visuais e exibir os campos já calculados pelo MSS.

## 2. Arquitetura e fluxo

```text
GET /api/v1/irrigation
        |
        v
Validar userId, devEUI e timezone
        |
        v
Consultar API de sensores
        |
        v
Validar, filtrar e ordenar leituras
        |
        +--> Kalman escalar: série histórica filtrada
        |
        +--> Kalman nível + tendência: projeção futura
        |
        +--> Diagnósticos: tendência, incerteza e limites persistentes
        |
        v
Consultar coordenadas do dispositivo no Supabase
        |
        v
Consultar seis dias de previsão na Open-Meteo
        |
        +--> Resumo meteorológico
        +--> Classificação do solo e recomendação
        |
        v
JSON com dados, projeção, diagnósticos e decisão
```

## 3. Requisitos

- Go 1.22 ou superior.
- API de sensores acessível pelo servidor Go.
- Projeto Supabase com registro do usuário e coluna JSON de dispositivos/coordenadas.
- Acesso à internet para consultar a Open-Meteo.

O serviço usa somente a biblioteca padrão do Go, portanto não há dependências externas no `go.mod`.

## 4. Configuração

Copie o arquivo de exemplo e preencha os valores do ambiente:

```bash
cp .env.example .env
```

Configure as variáveis na sessão de terminal antes de iniciar o serviço:

```bash
set -a
source .env
set +a
go test ./...
go vet ./...
go run .
```

O serviço escuta na porta `8456` por padrão.

### 4.1. Variáveis de ambiente

| Variável | Padrão | Descrição |
|---|---:|---|
| `PORT` | `8456` | Porta HTTP do microsserviço. |
| `SENSOR_API_URL_TEMPLATE` | — | Template da URL da API de sensores. Deve conter `{user}` e `{devEUI}`. |
| `SENSOR_API_TOKEN` | vazio | Token Bearer opcional enviado à API de sensores. |
| `SUPABASE_URL` | — | URL do projeto Supabase. |
| `SUPABASE_KEY` | — | Chave usada pelo servidor para consultar o Supabase. Mantenha-a fora do frontend. |
| `SUPABASE_TABLE` | `profiles` | Tabela que contém os dispositivos. |
| `SUPABASE_USER_COLUMN` | `id` | Coluna usada para identificar o usuário. |
| `SUPABASE_DEVICES_COLUMN` | `devices` | Coluna JSON que contém os dispositivos e coordenadas. |
| `SOIL_DRY_THRESHOLD` | `30` | Valor igual ou abaixo do qual o solo é classificado como seco. |
| `SOIL_WET_THRESHOLD` | `70` | Valor igual ou acima do qual o solo é classificado como úmido. |
| `RAIN_PROBABILITY_THRESHOLD` | `60` | Probabilidade diária a partir da qual `rain_likely` é verdadeiro. |
| `MIN_RAIN_MM` | `3` | Volume mínimo de chuva que participa da regra de cautela. |
| `KALMAN_PROCESS_NOISE` | `1` | Ruído do processo do filtro escalar. |
| `KALMAN_MEASUREMENT_NOISE` | `4` | Ruído de medição do filtro escalar. |
| `IRRIGATION_TIMEZONE` | `America/Sao_Paulo` | Fuso padrão; pode ser sobrescrito pela query string. |
| `SOIL_PREDICTION_HORIZON_HOURS` | `24` | Horizonte da projeção de umidade, de 1 a 168 horas. |
| `SOIL_PREDICTION_STEP_MINUTES` | `60` | Intervalo entre pontos da projeção, de 5 a 1440 minutos. O serviço limita a 500 pontos. |
| `SOIL_PREDICTION_PROCESS_NOISE` | `0.05` | Parâmetro de ruído de processo do modelo preditivo de nível + tendência. |
| `SOIL_MOISTURE_MIN_VALUE` | `0` | Limite inferior da escala usada para limitar a projeção e seus intervalos. |
| `SOIL_MOISTURE_MAX_VALUE` | `100` | Limite superior da escala usada para limitar a projeção e seus intervalos. |
| `SOIL_TREND_STABLE_THRESHOLD` | `0.1` | Módulo mínimo da tendência, em unidades do sensor por hora, para classificar alta ou queda. |
| `SOIL_PREDICTION_HIGH_UNCERTAINTY_WIDTH` | `30` | Largura do intervalo de projeção a partir da qual a incerteza é classificada como alta. |
| `SOIL_DIAGNOSTIC_CONSECUTIVE_SAMPLES` | `5` | Quantidade mínima de leituras válidas consecutivas para acionar diagnóstico de limite. Deve ser pelo menos 2. |
| `SOIL_ZERO_TOLERANCE` | `0.5` | Distância acima do limite inferior considerada próxima de zero. |
| `SOIL_SATURATION_MARGIN` | `1` | Distância abaixo do limite superior considerada próxima de saturação. |
| `TEMP_FIX_SENSOR_TIMESTAMP` | `false` | Ajuste temporário de timestamps para testes. **Não ativar em produção.** |

Os parâmetros relacionados à escala devem corresponder à escala real emitida pelo sensor. Os números padrão são valores iniciais, não limites agronômicos universais. As variâncias/ruídos do filtro precisam ser calibrados empiricamente.

### 4.2. API de sensores

O serviço aceita uma resposta com um registro ou com uma lista de registros. Exemplo:

```json
{
  "devEUI": "5e76ce4fd99eefe3",
  "name": "Sensor de umidade",
  "payload": {
    "soil_moisture": 45.0,
    "air_humidity": 60.0,
    "air_temperature": 25.5,
    "timestamp": 1790335177,
    "validity": true
  }
}
```

O `timestamp` deve ser Unix time em segundos. A leitura precisa ter `validity: true`, umidade numérica e timestamp positivo; leituras mais de cinco minutos no futuro ou mais antigas que 24 horas são descartadas. Se o campo `devEUI` vier preenchido, deve corresponder ao dispositivo solicitado. Leituras aceitas são ordenadas cronologicamente.

`TEMP_FIX_SENSOR_TIMESTAMP=true` desloca timestamps recebidos, preservando seus intervalos, para facilitar testes com dados antigos. Isso altera a referência temporal artificialmente e não deve ser usado em produção.

### 4.3. Localização no Supabase

A tabela e as colunas são configuradas pelas variáveis `SUPABASE_TABLE`, `SUPABASE_USER_COLUMN` e `SUPABASE_DEVICES_COLUMN`. A coluna `devices` pode ser um objeto indexado ou um array. Cada dispositivo deve conter `devEUI` e coordenadas com `lat`/`lon` ou `latitude`/`longitude`.

Exemplo:

```json
{
  "1": {
    "devEUI": "5e76ce4fd99eefe3",
    "lat": -23.6471338,
    "lon": -46.5741899
  }
}
```

## 5. Endpoints

### `GET /healthz`

Verifica se o servidor HTTP está ativo. Não valida a disponibilidade das APIs externas.

```bash
curl -i http://localhost:8456/healthz
```

Resposta esperada:

```json
{"status":"ok"}
```

### `GET /api/v1/irrigation`

Parâmetros:

| Parâmetro | Obrigatório | Descrição |
|---|---|---|
| `userId` | Sim | Identificador do usuário correspondente à coluna configurada em `SUPABASE_USER_COLUMN`. |
| `devEUI` | Sim | Identificador do dispositivo. |
| `timezone` | Não | Fuso IANA para datas da previsão; padrão `America/Sao_Paulo`. |

Exemplo:

```bash
curl -sS -G "http://localhost:8456/api/v1/irrigation" \
  --data-urlencode "userId=ID_DO_USUARIO" \
  --data-urlencode "devEUI=5e76ce4fd99eefe3" \
  --data-urlencode "timezone=America/Sao_Paulo"
```

O endpoint deve ser autenticado e autorizado antes de ser exposto em produção. Não confie apenas no `userId` informado pelo cliente.

## 6. Contrato da resposta JSON

A resposta HTTP `200` inclui os campos abaixo. Os campos legados `sensor`, `forecast_today_and_next_5_days`, `irrigation` e `note` continuam presentes; foram acrescentados históricos, projeção, diagnósticos e resumo meteorológico.

| Campo | Conteúdo |
|---|---|
| `sensor_id` | `devEUI` consultado. |
| `location` | Latitude, longitude, timezone e fonte da localização. |
| `sensor` | Última leitura bruta, valor filtrado atual, temperatura, umidade do ar, timestamp e validade. |
| `sensor_history` | Leituras válidas da janela recente, com valores brutos e filtrados calculados no Go. |
| `soil_moisture_prediction` | Modelo, nível atual, tendência, projeção, intervalo de incerteza e diagnósticos calculados no Go. |
| `forecast_today_and_next_5_days` | Até seis dias de probabilidade de chuva, precipitação e ET₀. |
| `forecast_summary` | Agregados meteorológicos calculados no Go. |
| `irrigation` | Classificação do solo, ação, recomendação, sinalização de validação manual e mensagem. |
| `note` | Limitações importantes do modelo. |

### 6.1. Exemplo da estrutura dos novos campos

Este é um exemplo estrutural, com valores ilustrativos:

```json
{
  "sensor_id": "5e76ce4fd99eefe3",
  "sensor": {
    "timestamp": "2026-10-22T16:35:58Z",
    "soil_moisture_raw": 42.1,
    "soil_moisture_kalman_filtered": 41.6,
    "air_humidity_percent": 65.0,
    "air_temperature_celsius": 24.2,
    "validity": true
  },
  "sensor_history": [
    {
      "timestamp": "2026-10-22T16:30:00Z",
      "soil_moisture_raw": 43.0,
      "soil_moisture_kalman_filtered": 42.4
    }
  ],
  "soil_moisture_prediction": {
    "model": "kalman_level_trend",
    "unit": "sensor_scale",
    "base_timestamp": "2026-10-22T16:35:58Z",
    "horizon_hours": 24,
    "step_minutes": 60,
    "current_filtered": 41.6,
    "trend_per_hour": -0.2,
    "trend_status": "decreasing",
    "predicted_at_horizon": 36.8,
    "change_from_current": -4.8,
    "prediction_interval_width": 28.4,
    "confidence_status": "within_threshold",
    "diagnostics": {
      "persistent_zero": false,
      "sensor_saturated": false,
      "consecutive_near_zero": 0,
      "consecutive_near_maximum": 0,
      "required_consecutive_samples": 5,
      "messages": [
        "Nenhuma condição de diagnóstico foi acionada pelos limites configurados."
      ]
    },
    "points": [
      {
        "timestamp": "2026-10-22T16:35:58Z",
        "hours_ahead": 0,
        "soil_moisture_predicted": 41.6,
        "lower_bound": 40.0,
        "upper_bound": 43.2
      }
    ]
  },
  "forecast_today_and_next_5_days": [
    {
      "date": "2026-10-22",
      "precipitation_probability_percent": 35,
      "precipitation_mm": 1.2,
      "et0_mm": 3.2,
      "rain_likely": false
    }
  ],
  "forecast_summary": {
    "days": 1,
    "total_precipitation_mm": 1.2,
    "max_precipitation_probability_percent": 35,
    "days_with_likely_rain": 0,
    "et0_available": true,
    "total_et0_mm": 3.2
  },
  "irrigation": {
    "soil_status": "intermediate",
    "action": "manual_check",
    "should_irrigate": null,
    "should_irrigate_today": null,
    "recommended_day": null,
    "requires_manual_validation": true,
    "message": "A leitura está entre os limiares; calibre o sensor e verifique a cultura antes de decidir."
  },
  "note": "A projeção é estatística e exige validação de campo."
}
```

Os exemplos acima mostram a forma dos objetos e **não representam a saída de uma consulta real**. Na resposta real, as listas incluem todos os pontos que o processamento aceitou ou gerou.

### 6.2. `sensor_history`

Cada ponto tem:

- `timestamp`: horário UTC da medição;
- `soil_moisture_raw`: medição recebida da API de sensores;
- `soil_moisture_kalman_filtered`: estimativa filtrada pelo MSS.

O array contém leituras que passaram pela validação e pela janela de idade configurada no serviço (24 horas na versão atual). Não é o histórico de sete dias do cliente.

### 6.3. `soil_moisture_prediction`

| Campo | Interpretação |
|---|---|
| `model` | Identificador do modelo preditivo (`kalman_level_trend`). |
| `unit` | `sensor_scale`: escala numérica do sensor, não necessariamente porcentagem volumétrica do solo. |
| `base_timestamp` | Data/hora usada como origem da projeção. |
| `horizon_hours` | Horizonte previsto. |
| `step_minutes` | Intervalo entre pontos. |
| `current_filtered` | Valor de referência filtrado no início da projeção. |
| `trend_per_hour` | Tendência estimada por hora, na escala do sensor. |
| `trend_status` | `increasing`, `decreasing` ou `stable`, conforme `SOIL_TREND_STABLE_THRESHOLD`. |
| `predicted_at_horizon` | Estimativa central no último ponto do horizonte. |
| `change_from_current` | Diferença calculada no Go entre o valor projetado no horizonte e o atual filtrado. |
| `prediction_interval_width` | Largura do intervalo no final do horizonte (`upper_bound - lower_bound`). |
| `confidence_status` | `high_uncertainty`, `within_threshold` ou `unavailable`. |
| `diagnostics` | Diagnósticos de leituras próximas aos limites físicos configurados e mensagens calculadas pelo Go. |
| `points` | Pontos futuros contendo timestamp, horas à frente, valor previsto e limites. |

A estimativa futura é estatística, baseada na tendência recente das medições. O modelo atual não incorpora chuva futura, ET₀, água aplicada, drenagem, tipo de solo ou espécie cultivada. A faixa é uma aproximação derivada das covariâncias do modelo; não é uma garantia probabilística calibrada em campo.

### 6.4. Diagnósticos do solo

`soil_moisture_prediction.diagnostics` contém:

- `persistent_zero`: as últimas N leituras válidas ficaram dentro da tolerância configurada próxima ao limite inferior;
- `sensor_saturated`: as últimas N leituras válidas ficaram dentro da margem configurada próxima ao limite superior;
- `consecutive_near_zero`: quantidade de amostras consecutivas recentes próximas ao limite inferior;
- `consecutive_near_maximum`: quantidade de amostras consecutivas recentes próximas ao limite superior;
- `required_consecutive_samples`: N mínimo configurado;
- `messages`: mensagens explicativas geradas no backend.

Os diagnósticos se baseiam nas **medições brutas recentes**, não no valor filtrado. Uma sequência no limite pode representar uma condição física real ou uma leitura presa por escala, calibração ou instalação. O diagnóstico, isoladamente, não prova defeito.

### 6.5. `confidence_status`

O serviço mede a largura do intervalo no último ponto da projeção. Se ela for maior ou igual a `SOIL_PREDICTION_HIGH_UNCERTAINTY_WIDTH`, retorna `high_uncertainty`; caso contrário, `within_threshold`. Se não houver pontos projetados, retorna `unavailable`.

Esse campo é um critério operacional configurável de largura de intervalo, não uma probabilidade estatística de acerto.

### 6.6. `forecast_summary`

| Campo | Interpretação |
|---|---|
| `days` | Quantidade de dias no resumo. |
| `total_precipitation_mm` | Soma da precipitação prevista na janela retornada. |
| `max_precipitation_probability_percent` | Maior probabilidade diária na janela. |
| `days_with_likely_rain` | Número de dias que atingem `RAIN_PROBABILITY_THRESHOLD`. |
| `et0_available` | Indica se a API disponibilizou ET₀ para todos os dias do resumo. |
| `total_et0_mm` | Soma diária de ET₀ quando o dado está disponível; omitido quando indisponível. |

Probabilidade de chuva, volume previsto e ET₀ são variáveis diferentes. A ET₀ não participa diretamente da pontuação de escolha do dia na implementação atual.

### 6.7. `irrigation`

Os valores de `soil_status` são:

- `dry`: valor filtrado menor ou igual a `SOIL_DRY_THRESHOLD`;
- `intermediate`: valor entre os limites;
- `wet`: valor filtrado maior ou igual a `SOIL_WET_THRESHOLD`.

Quando o solo está seco e não há diagnóstico persistente de limite, a API escolhe o dia com a menor pontuação:

```text
score = probabilidade_de_chuva + min(3 * precipitação_mm, 30)
```

Se a probabilidade e o volume atingirem seus limites de cautela, a ação será `irrigate_with_caution`; caso contrário, será `irrigate_on_recommended_day`.

**Regra conservadora para leituras persistentes no limite:** se `persistent_zero` ou `sensor_saturated` for verdadeiro, a API retorna `action: "manual_check_sensor"`, `requires_manual_validation: true` e campos de decisão automática como `should_irrigate`, `should_irrigate_today` e `recommended_day` como `null`. Isso evita transformar uma sequência potencialmente anômala em uma recomendação automática. A classificação `soil_status` permanece disponível como descrição numérica da leitura.

Na faixa intermediária, a API retorna `action: "manual_check"` e `requires_manual_validation: true`. Quando o solo está úmido, retorna `not_needed_now`, sem data recomendada.

Essas regras não acionam irrigação automaticamente e não substituem validação agronômica.

## 7. Filtros e projeção

### 7.1. Kalman escalar para histórico

O filtro escalar reduz oscilações de leituras sucessivas. Para cada medição `z`, ele calcula a covariância predita, o ganho de Kalman e atualiza a estimativa. Os parâmetros `KALMAN_PROCESS_NOISE` e `KALMAN_MEASUREMENT_NOISE` precisam refletir a variabilidade e a escala real do sensor.

Esse filtro produz a série `soil_moisture_kalman_filtered` em `sensor_history` e o valor atual usado na classificação de `soil_status`.

### 7.2. Kalman de nível + tendência

Um modelo de dois estados estima simultaneamente:

- nível de umidade;
- inclinação temporal em unidades do sensor por hora.

Os intervalos entre leituras são calculados a partir dos timestamps reais, em horas. O estado e a covariância são atualizados em cada nova medição. A projeção usa o último valor filtrado como origem e a tendência estimada para gerar os pontos futuros. As estimativas e os limites são limitados a `SOIL_MOISTURE_MIN_VALUE` e `SOIL_MOISTURE_MAX_VALUE`.

`trend_status` é baseado em `trend_per_hour`:

- `increasing`: tendência maior que o limiar positivo;
- `decreasing`: tendência menor que o limiar negativo;
- `stable`: tendência dentro do limiar.

O modelo não deve inventar uma mudança quando as medições não mostram tendência. Por exemplo, se todas as últimas medições forem zero, a projeção central pode continuar em zero, enquanto a incerteza aumenta. Isso é um alerta para revisar a escala e a instalação do sensor, não evidência de que o solo continuará exatamente nesse valor.

## 8. Testes

Execute:

```bash
go test ./...
go vet ./...
```

A suíte cobre a classificação da tendência, detecção de sequências persistentes próximas aos limites, comportamento conservador da recomendação, resumo meteorológico, limites da projeção e classificação de incerteza.

## 9. Erros HTTP

| Código | Significado |
|---|---|
| `200 OK` | Processamento concluído. |
| `400 Bad Request` | `userId` ou `devEUI` ausente, ou fuso horário inválido. |
| `502 Bad Gateway` | Falha ao obter/validar dados da API de sensores, Supabase ou Open-Meteo. |

Exemplo de erro:

```json
{"error":"API não retornou leitura válida e recente para esse sensor"}
```

Um `502` pode indicar indisponibilidade externa, dados ausentes ou ausência de leituras válidas; não significa necessariamente que o cálculo interno falhou.

## 10. Segurança e implantação

- Nunca coloque `SUPABASE_KEY` ou `SENSOR_API_TOKEN` no aplicativo Expo ou no site.
- Exija autenticação e autorização para o endpoint antes de publicação multiusuário.
- Valide no servidor se o usuário autenticado pode consultar o `devEUI` enviado.
- Configure HTTPS, logs e timeouts para o ambiente de produção.
- Não habilite `TEMP_FIX_SENSOR_TIMESTAMP` em produção.
- Não use a recomendação como acionamento automático sem validação agronômica e testes controlados.

## 11. Limitações conhecidas

- Os limiares de solo e a escala do sensor precisam de calibração por equipamento, instalação, substrato e cultura.
- Kalman reduz ruído, mas não corrige viés de calibração ou sensor defeituoso.
- A projeção é estatística e ainda não calcula balanço hídrico.
- A previsão da Open-Meteo tem incerteza e resolução espacial próprias.
- ET₀ é informativa; ainda não entra na pontuação do dia recomendado.
- O serviço não calcula lâmina, volume necessário ou duração de irrigação.
- Os resultados devem ser comparados com observações e decisões de referência em campo.
