# Irrigacao-MSS

Microsserviço em Go para análise de dados de sensores de umidade do solo e geração de recomendações de irrigação com base nas condições atuais do solo, na tendência estimada de umidade e na previsão meteorológica para os próximos seis dias.

O serviço integra leituras de sensores, dados de localização armazenados no Supabase e informações meteorológicas do Open-Meteo. A partir dessas informações, filtra as medições, estima a evolução da umidade do solo e recomenda se a irrigação deve ser evitada, avaliada manualmente ou programada para um dia mais adequado.

O `Irrigacao-MSS` é um serviço de **apoio à decisão**: ele fornece informações e recomendações, mas não aciona diretamente bombas, válvulas ou qualquer outro equipamento de irrigação.

---

## Sumário

1. [Visão geral da arquitetura](#1-visão-geral-da-arquitetura)
2. [Tecnologias utilizadas](#2-tecnologias-utilizadas)
3. [Requisitos](#3-requisitos)
4. [Configuração das variáveis de ambiente](#4-configuração-das-variáveis-de-ambiente)
5. [Execução do microsserviço](#5-execução-do-microsserviço)
6. [Endpoints disponíveis](#6-endpoints-disponíveis)
7. [Como realizar uma requisição](#7-como-realizar-uma-requisição)
8. [Estrutura da resposta](#8-estrutura-da-resposta)
9. [Funcionamento do algoritmo](#9-funcionamento-do-algoritmo)
10. [Interpretação das recomendações](#10-interpretação-das-recomendações)
11. [Tratamento e validação dos dados](#11-tratamento-e-validação-dos-dados)
12. [Códigos de resposta HTTP](#12-códigos-de-resposta-http)
13. [Limitações e considerações](#13-limitações-e-considerações)
14. [Solução de problemas](#14-solução-de-problemas)
15. [Descrição metodológica para artigo científico](#15-descrição-metodológica-para-artigo-científico)

---

## 1. Visão geral da arquitetura

O `Irrigacao-MSS` utiliza diferentes fontes de informação para produzir a recomendação de irrigação.

O fluxo de processamento é composto pelas seguintes etapas:

1. Recebimento da solicitação com o identificador do usuário e o identificador do sensor (`devEUI`).
2. Consulta à API de sensores para recuperar as leituras recentes do dispositivo.
3. Validação das leituras recebidas e ordenação cronológica dos registros válidos.
4. Aplicação de um filtro de Kalman para suavizar as medições de umidade do solo.
5. Consulta ao Supabase para localizar as coordenadas geográficas associadas ao dispositivo.
6. Consulta ao Open-Meteo para obter a previsão meteorológica dos próximos seis dias.
7. Aplicação de um modelo de Kalman com estimativa de nível e tendência para projetar a evolução da umidade do solo nas próximas 24 horas.
8. Avaliação das condições do sensor, classificação do estado atual do solo e análise das condições meteorológicas.
9. Geração de uma resposta estruturada com histórico, estimativa de umidade, previsão meteorológica e recomendação de irrigação.

### 1.1. Diagrama simplificado

```text
Frontend / Aplicação cliente
            |
            v
API Service
GET /api/v1/irrigation
            |
            v
Irrigacao-MSS (Go)
            |
            +----------------------+
            |                      |
            v                      v
API de sensores                 Supabase
Leituras recentes               Localização do dispositivo
            |                      |
            +-----------+----------+
                        |
                        v
                Processamento dos dados
                        |
              +---------+----------+
              |                    |
              v                    v
        Filtro de Kalman     Open-Meteo
        Histórico e tendência Previsão meteorológica
              |                    |
              +---------+----------+
                        |
                        v
              Recomendação de irrigação
                        |
                        v
              Resposta JSON ao cliente
```

Na arquitetura em que o `API Service` atua como ponto de entrada público, o frontend deve consultar esse serviço. O `Irrigacao-MSS` é chamado internamente pelo `API Service`, evitando a necessidade de expor diretamente a porta do microsserviço ao cliente.

### 1.2. Responsabilidades dos componentes

| Componente | Responsabilidade |
|---|---|
| Frontend | Enviar os parâmetros necessários e apresentar os dados retornados. |
| API Service | Disponibilizar o endpoint público e encaminhar a solicitação ao microsserviço de irrigação. |
| Irrigacao-MSS | Validar os dados, executar os cálculos e produzir a recomendação. |
| API de sensores | Fornecer as leituras recentes associadas ao usuário e ao dispositivo. |
| Supabase | Armazenar os dados do usuário e as informações de localização dos dispositivos. |
| Open-Meteo | Fornecer os dados da previsão meteorológica utilizados na análise. |

---

## 2. Tecnologias utilizadas

- **Go:** implementação do microsserviço e das rotinas de processamento.
- **Supabase:** consulta aos dados de usuários e dispositivos.
- **API de sensores:** obtenção das leituras recentes do sensor.
- **Open-Meteo:** obtenção de dados meteorológicos.
- **JSON:** formato de comunicação entre os serviços.
- **HTTP:** protocolo utilizado para as requisições e respostas.

O projeto utiliza a biblioteca `github.com/supabase-community/supabase-go` para a integração com o Supabase. Portanto, o gerenciamento das dependências deve ser realizado pelo módulo Go do projeto (`go.mod` e `go.sum`).

---

## 3. Requisitos

Para executar o microsserviço, são necessários:

- Go instalado e compatível com a versão especificada no `go.mod`;
- acesso à API de sensores;
- acesso à instância Supabase;
- credenciais e permissões adequadas para consultar os dados necessários;
- acesso à internet para consultar o Open-Meteo;
- conectividade de rede entre os serviços quando executados em contêineres.

---

## 4. Configuração das variáveis de ambiente

O serviço utiliza variáveis de ambiente para definir a porta, as integrações e os limiares empregados na análise.

### 4.1. Variáveis obrigatórias

| Variável | Descrição |
|---|---|
| `SENSOR_API_URL_TEMPLATE` | Modelo da URL utilizada para consultar a API de sensores. Deve conter `{user}` e `{devEUI}`. |
| `SUPABASE_URL` | URL da instância Supabase. |
| `SUPABASE_KEY` | Chave utilizada pelo backend para acessar o Supabase. Deve ser mantida em segredo. |

Exemplo do modelo da URL da API de sensores:

```env
SENSOR_API_URL_TEMPLATE=http://api-service:3000/api/sensors/latest/{user}/{devEUI}
```

Nesse exemplo, `api-service` é o nome de serviço utilizado na rede interna do Docker Compose. Ajuste o endereço e a porta conforme o ambiente em que a API de sensores estiver sendo executada.

A chave do Supabase deve ser configurada apenas no ambiente do servidor. Não a exponha no frontend, não a inclua em repositórios públicos e não a registre em logs.

### 4.2. Configuração geral

| Variável | Valor padrão | Descrição |
|---|---|---|
| `PORT` | `8456` | Porta HTTP do microsserviço. |
| `SUPABASE_TABLE` | `users` | Tabela consultada para localizar o usuário. |
| `SUPABASE_USER_COLUMN` | `userId` | Coluna que identifica o usuário. |
| `SUPABASE_DEVICES_COLUMN` | `devices` | Coluna JSON que armazena os dispositivos e suas informações. |

Os valores padrão acima correspondem à configuração utilizada pelo serviço. Caso sejam definidos valores diferentes no ambiente, eles devem corresponder à estrutura real do banco de dados.

### 4.3. Limiares de classificação do solo

| Variável | Valor padrão | Descrição |
|---|---:|---|
| `SOIL_DRY_THRESHOLD` | `30` | Limite inferior utilizado para classificar o solo como seco. |
| `SOIL_WET_THRESHOLD` | `70` | Limite superior utilizado para classificar o solo como úmido. |

Os valores devem ser interpretados na escala de umidade fornecida pelo sensor. A calibração precisa considerar o equipamento utilizado, o tipo de solo e as condições de instalação.

Com os valores padrão, a interpretação geral é:

- umidade menor ou igual ao limiar seco: solo classificado como seco;
- umidade maior ou igual ao limiar úmido: solo classificado como úmido;
- umidade entre os limiares: condição intermediária, que requer avaliação adicional.

Esses limiares são parâmetros operacionais e não representam valores universais para todos os solos ou culturas.

### 4.4. Limiares meteorológicos

| Variável | Valor padrão | Descrição |
|---|---:|---|
| `RAIN_PROBABILITY_THRESHOLD` | `60` | Probabilidade mínima de chuva considerada relevante para a regra de cautela. |
| `MIN_RAIN_MM` | `3` | Precipitação mínima prevista, em milímetros, considerada relevante para a regra de cautela. |

Na regra de cautela da implementação atual, a probabilidade de chuva precisa alcançar o limiar configurado e a precipitação prevista precisa alcançar o volume mínimo configurado.

### 4.5. Parâmetros do filtro de Kalman

| Variável | Valor padrão | Descrição |
|---|---:|---|
| `KALMAN_PROCESS_NOISE` | `1` | Ruído de processo utilizado pelo filtro aplicado às medições. |
| `KALMAN_MEASUREMENT_NOISE` | `4` | Ruído de medição utilizado pelo filtro aplicado às medições. |

Esses parâmetros influenciam o equilíbrio entre a leitura recebida do sensor e a estimativa filtrada.

### 4.6. Parâmetros da projeção de umidade

| Variável | Valor padrão | Descrição |
|---|---:|---|
| `SOIL_PREDICTION_HORIZON_HOURS` | `24` | Horizonte futuro da projeção de umidade, em horas. |
| `SOIL_PREDICTION_STEP_MINUTES` | `60` | Intervalo entre pontos consecutivos da projeção, em minutos. |
| `SOIL_PREDICTION_PROCESS_NOISE` | `0.05` | Ruído de processo utilizado pelo modelo de projeção. |
| Limite mínimo da projeção | `0` | Limite inferior aplicado aos valores projetados. |
| Limite máximo da projeção | `100` | Limite superior aplicado aos valores projetados. |
| `SOIL_TREND_STABLE_THRESHOLD` | `0.1` | Limite utilizado para classificar a tendência como estável. |
| `SOIL_PREDICTION_HIGH_UNCERTAINTY_WIDTH` | `30` | Referência para sinalizar intervalos de previsão com alta incerteza. |

Os limites de `0` a `100` pressupõem uma escala compatível com essa faixa. Caso o sensor utilize outra escala, a calibração e os limites devem ser revistos.

### 4.7. Parâmetros de diagnóstico do sensor

| Variável | Valor padrão | Descrição |
|---|---:|---|
| `SOIL_DIAGNOSTIC_CONSECUTIVE_SAMPLES` | `5` | Quantidade consecutiva de leituras considerada na identificação de padrões persistentes. |
| `SOIL_ZERO_TOLERANCE` | `0.5` | Tolerância usada na detecção de leituras próximas de zero. |
| `SOIL_SATURATION_MARGIN` | `1` | Margem usada na detecção de leituras próximas do limite máximo da escala. |

Esses parâmetros permitem sinalizar padrões que podem indicar um problema de medição, como leituras persistentemente nulas ou próximas da saturação da escala.

> **Observação sobre timestamps:** na versão atual do código revisado, a correção temporária dos timestamps está definida diretamente como habilitada. Isso significa que os registros podem ter seus timestamps ajustados para que o registro mais recente coincida com o horário atual, preservando os intervalos entre as leituras. Essa lógica não deve ser considerada uma correção permanente dos dados do sensor. Antes de utilizar o serviço em produção, o comportamento deve ser corrigido para ser explicitamente configurável ou removido, de modo que os horários reais sejam preservados.

---

## 5. Execução do microsserviço

### 5.1. Execução local

Configure as variáveis de ambiente obrigatórias antes de iniciar o serviço.

Exemplo de configuração para um terminal Linux:

```bash
export PORT=8456

export SENSOR_API_URL_TEMPLATE="http://localhost:3000/api/sensors/latest/{user}/{devEUI}"

export SUPABASE_URL="https://SEU-PROJETO.supabase.co"
export SUPABASE_KEY="SUA_CHAVE_SUPABASE"
```

Os endereços e as credenciais são exemplos. Substitua-os pelos valores do seu ambiente.

Na raiz do projeto, instale ou valide as dependências:

```bash
go mod tidy
```

Em seguida, inicie o microsserviço:

```bash
go run .
```

Se o projeto estiver estruturado com o ponto de entrada em outro diretório, execute o comando correspondente à estrutura real do repositório.

A aplicação deverá iniciar utilizando a porta definida em `PORT`, que é `8456` por padrão.

### 5.2. Execução com Docker Compose

Em um ambiente com vários microsserviços, configure a URL da API de sensores utilizando o nome do serviço na rede interna do Docker Compose.

Exemplo:

```env
SENSOR_API_URL_TEMPLATE=http://api-service:3000/api/sensors/latest/{user}/{devEUI}
```

O `Irrigacao-MSS` deve compartilhar uma rede capaz de alcançar a API de sensores. A configuração de `IRRIGATION_MSS_URL` no serviço intermediário deve apontar para o endereço interno do microsserviço, por exemplo:

```env
IRRIGATION_MSS_URL=http://irrigacao-mss:8456
```

Os nomes `api-service` e `irrigacao-mss` devem corresponder aos nomes reais dos serviços no arquivo `compose.yaml` ou `docker-compose.yml`.

---

## 6. Endpoints disponíveis

### 6.1. Verificação de saúde

**Endpoint:**

```http
GET /healthz
```

Esse endpoint permite verificar se o processo HTTP está respondendo.

Exemplo:

```bash
curl http://localhost:8456/healthz
```

Resposta esperada:

```json
{
  "status": "ok"
}
```

Esse resultado confirma que o endpoint de saúde está acessível. Ele não garante, por si só, que as integrações com Supabase, API de sensores e Open-Meteo estejam funcionando.

### 6.2. Recomendação de irrigação

**Endpoint:**

```http
GET /api/v1/irrigation
```

Parâmetros de consulta:

| Parâmetro | Obrigatório | Descrição |
|---|---|---|
| `userId` | Sim | Identificador do usuário associado ao dispositivo. |
| `devEUI` | Sim | Identificador único do dispositivo sensor. |
| `timezone` | Não | Fuso horário utilizado no processamento das datas. O padrão é `America/Sao_Paulo`. |

A solicitação retorna dados sobre a leitura mais recente, o histórico recente, a projeção da umidade para as próximas 24 horas, a previsão meteorológica dos próximos seis dias e a recomendação de irrigação.

---

## 7. Como realizar uma requisição

### 7.1. Consultando diretamente o microsserviço

Se a porta `8456` estiver acessível no ambiente de execução, a chamada pode ser realizada assim:

```bash
curl -G "http://localhost:8456/api/v1/irrigation" \
  --data-urlencode "userId=SEU_USER_ID" \
  --data-urlencode "devEUI=SEU_DEV_EUI" \
  --data-urlencode "timezone=America/Sao_Paulo"
```

Substitua `SEU_USER_ID` e `SEU_DEV_EUI` pelos identificadores reais.

O parâmetro `timezone` pode ser omitido quando o fuso padrão for adequado.

### 7.2. Consultando pela API Service

Quando o `API Service` atua como gateway público, utilize o endpoint disponibilizado por ele:

```bash
curl -G "http://localhost:3000/api/v1/irrigation" \
  --data-urlencode "userId=SEU_USER_ID" \
  --data-urlencode "devEUI=SEU_DEV_EUI" \
  --data-urlencode "timezone=America/Sao_Paulo"
```

Nesse fluxo, o `API Service` encaminha a solicitação ao `Irrigacao-MSS`, que realiza os cálculos e devolve a resposta.

Para o frontend, o endereço de acesso deve ser o da API pública configurada para a aplicação, e não necessariamente a porta interna do microsserviço.

---

## 8. Estrutura da resposta

A resposta é um objeto JSON dividido em seções. Cada seção possui uma responsabilidade específica na interpretação do resultado.

### 8.1. Principais campos

| Campo | Descrição |
|---|---|
| `sensor_id` | Identificador do sensor consultado. |
| `location` | Coordenadas, fuso horário e origem dos dados de localização. |
| `sensor` | Informações da leitura mais recente, incluindo umidade bruta e filtrada. |
| `sensor_history` | Leituras recentes válidas utilizadas no processamento. |
| `soil_moisture_prediction` | Projeção da umidade do solo para o horizonte configurado. |
| `forecast_today_and_next_5_days` | Previsão meteorológica diária para seis dias. |
| `forecast_summary` | Resumo consolidado da previsão meteorológica. |
| `irrigation` | Recomendação gerada pelo algoritmo. |
| `note` | Observação geral sobre o resultado. |

### 8.2. Localização

O campo `location` contém informações como:

- `latitude`: latitude associada ao dispositivo;
- `longitude`: longitude associada ao dispositivo;
- `timezone`: fuso horário aplicado à resposta;
- `source`: origem dos dados de localização.

Na integração atual, as coordenadas são obtidas do registro do dispositivo no Supabase. O campo `source` identifica essa origem, indicada como `Supabase`.

### 8.3. Dados do sensor

O campo `sensor` contém a leitura mais recente considerada válida e os valores usados no processamento.

Os campos incluem:

- `timestamp`: instante associado à leitura, em formato de data e hora ISO 8601;
- `soil_moisture_raw`: valor bruto da umidade;
- `soil_moisture_kalman_filtered`: valor de umidade após o filtro de Kalman;
- `air_humidity_percent`: umidade relativa do ar, quando disponibilizada;
- `air_temperature_celsius`: temperatura do ar, quando disponibilizada;
- `validity`: indicador de validade informado pelo sensor.

A umidade bruta representa o valor informado pelo dispositivo. A umidade filtrada é uma estimativa suavizada, que reduz a influência de variações isoladas das medições.

### 8.4. Histórico do sensor

O campo `sensor_history` apresenta as leituras recentes válidas, em ordem cronológica.

Cada ponto do histórico contém o timestamp da leitura, a umidade bruta e o valor filtrado correspondente.

Esse histórico permite observar a variação recente da umidade e entender em que dados a projeção foi baseada. O conjunto de registros disponível depende do que a API de sensores retorna e das regras de validade e recência aplicadas pelo microsserviço.

**Importante:** o histórico retornado não representa necessariamente todo o histórico de operação do sensor. Ele contém os registros recentes recebidos pela integração e aprovados pelas validações.

### 8.5. Projeção da umidade do solo

O campo `soil_moisture_prediction` descreve a estimativa da evolução da umidade nas próximas 24 horas, conforme o horizonte configurado.

Entre seus campos estão:

| Campo | Descrição |
|---|---|
| `model` | Identificador do modelo de projeção utilizado. |
| `unit` | Escala de referência dos valores projetados. |
| `base_timestamp` | Timestamp que serve como referência para a projeção. |
| `horizon_hours` | Horizonte futuro da projeção, em horas. |
| `step_minutes` | Intervalo entre pontos consecutivos. |
| `current_filtered` | Valor filtrado usado como condição inicial. |
| `trend_per_hour` | Tendência estimada de variação da umidade por hora. |
| `trend_status` | Classificação da tendência como crescente, decrescente ou estável. |
| `predicted_at_horizon` | Valor estimado ao final do horizonte. |
| `change_from_current` | Diferença entre a projeção final e a estimativa atual. |
| `prediction_interval_width` | Largura do intervalo de previsão no horizonte final. |
| `confidence_status` | Classificação da incerteza da projeção. |
| `diagnostics` | Indicadores de possíveis padrões anormais nas leituras. |
| `points` | Série temporal com os valores projetados e seus limites. |

Cada ponto de `points` contém um timestamp, a quantidade de horas à frente e a estimativa da umidade do solo. Os limites inferior e superior representam o intervalo associado à projeção.

O valor projetado deve ser interpretado como uma estimativa matemática, não como uma medição futura garantida.

### 8.6. Previsão meteorológica

O campo `forecast_today_and_next_5_days` contém a previsão diária para seis dias: o dia atual e os cinco dias seguintes.

A previsão utiliza informações do Open-Meteo, incluindo:

- precipitação diária prevista, em milímetros;
- probabilidade máxima de precipitação, em porcentagem;
- evapotranspiração de referência ET₀ FAO, quando disponível.

A previsão meteorológica auxilia a avaliação de quando a irrigação pode ser menos necessária ou quando existe risco de irrigar pouco antes de uma chuva relevante.

A disponibilidade de ET₀ é tratada separadamente. A ausência desse dado não significa, necessariamente, que a previsão de precipitação esteja indisponível.

### 8.7. Resumo meteorológico

O campo `forecast_summary` reúne estatísticas da previsão de seis dias:

| Campo | Descrição |
|---|---|
| `days` | Quantidade de dias de previsão considerados. |
| `total_precipitation_mm` | Soma da precipitação prevista no período. |
| `max_precipitation_probability_percent` | Maior probabilidade diária de precipitação no período. |
| `days_with_likely_rain` | Quantidade de dias que satisfazem a regra adotada para chuva relevante. |
| `et0_available` | Indica se os dados de ET₀ estão disponíveis. |
| `total_et0_mm` | Soma da ET₀ no período, quando disponível. |

Em uma resposta completa bem-sucedida, a previsão inclui seis dias. O campo `total_et0_mm` pode ser omitido quando não há dados de ET₀ disponíveis.

### 8.8. Exemplo ilustrativo de interpretação

Considere uma resposta com os seguintes resultados:

- umidade bruta: `45`;
- umidade filtrada: `45`;
- tendência: `stable`;
- umidade projetada ao final de 24 horas: `45`;
- estado do solo: intermediário;
- ação: `manual_check`;
- `should_irrigate`: `null`.

Essa resposta indica que o valor atual está entre os limiares padrão de solo seco e úmido. Nesse caso, o algoritmo não recomenda automaticamente ligar ou desligar a irrigação: ele solicita uma avaliação complementar.

O exemplo é ilustrativo. A resposta real contém os campos adicionais, as séries temporais e as seis entradas diárias da previsão meteorológica.

---

## 9. Funcionamento do algoritmo

O algoritmo combina processamento das medições, projeção de umidade e análise de previsão meteorológica. As etapas são descritas a seguir.

### 9.1. Recuperação das leituras

O microsserviço consulta a API de sensores utilizando o usuário e o `devEUI` informados na requisição.

A API de sensores pode retornar uma leitura ou uma coleção de leituras. Os registros são interpretados e submetidos às verificações de validade antes de serem utilizados nos cálculos.

### 9.2. Validação das leituras

Antes do processamento, o serviço verifica critérios como:

- correspondência do `devEUI`, quando o identificador está presente no registro;
- presença de timestamp positivo;
- ausência de timestamp mais de cinco minutos à frente do horário atual;
- recência da leitura, limitada a 24 horas;
- indicador `validity` igual a `true`;
- presença de um valor numérico de umidade do solo.

Os registros que não satisfazem os critérios exigidos são descartados.

Depois da validação, as leituras são ordenadas cronologicamente, de modo que o processamento respeite a sequência temporal das medições.

### 9.3. Filtragem da umidade com Kalman

As medições de um sensor podem apresentar pequenas oscilações causadas por ruído, variações instantâneas ou características do próprio sistema de aquisição.

Para reduzir o efeito dessas oscilações, o serviço aplica um filtro de Kalman unidimensional às leituras válidas.

O filtro estima o estado do sistema a partir de duas informações principais:

- previsão do estado anterior;
- nova medição recebida.

A contribuição de cada uma depende da incerteza associada ao estado estimado e ao ruído de medição.

Na implementação atual, os parâmetros padrão são:

```text
KALMAN_PROCESS_NOISE      = 1
KALMAN_MEASUREMENT_NOISE  = 4
```

O resultado é uma série filtrada que tende a apresentar menor sensibilidade a variações isoladas, mantendo a evolução temporal observada nas leituras.

Esse processamento não substitui a calibração do sensor. Ele reduz ruídos no sinal recebido, mas não corrige automaticamente erros sistemáticos, instalação inadequada ou defeitos físicos.

### 9.4. Estimativa da tendência da umidade

Além da filtragem das medições, o serviço utiliza um modelo de Kalman com dois componentes conceituais:

1. **Nível:** estimativa do valor atual da umidade.
2. **Tendência:** estimativa da direção e da velocidade da variação ao longo do tempo.

Com base no nível e na tendência estimados, o algoritmo projeta possíveis valores futuros da umidade.

O horizonte padrão é de 24 horas, com um ponto a cada 60 minutos.

Os valores são limitados à faixa configurada, de `0` a `100`, para evitar que a projeção ultrapasse os limites da escala considerada.

A projeção também calcula um intervalo de incerteza. Esse intervalo tende a ampliar-se à medida que o horizonte futuro aumenta, pois a incerteza da estimativa se acumula ao longo do tempo.

Quando a largura do intervalo final ultrapassa a referência configurada para alta incerteza, o resultado é sinalizado como `high_uncertainty`.

A projeção tem finalidade indicativa: ela descreve uma tendência estimada a partir das leituras disponíveis e não representa uma garantia de comportamento futuro. A implementação descrita não deve ser interpretada como um modelo físico completo do balanço hídrico do solo.

### 9.5. Classificação da tendência

A tendência é determinada a partir da variação estimada da umidade por hora.

O campo `trend_status` pode assumir as seguintes classificações:

| Valor | Interpretação |
|---|---|
| `increasing` | A estimativa indica aumento da umidade. |
| `decreasing` | A estimativa indica redução da umidade. |
| `stable` | A variação estimada está dentro do limite considerado estável. |

O limite padrão para considerar uma tendência estável é `0.1`, conforme `SOIL_TREND_STABLE_THRESHOLD`.

Essa classificação é feita a partir do comportamento estimado pelo modelo. Uma tendência crescente, por exemplo, não comprova por si só que o aumento decorra de chuva: o indicador depende das leituras processadas e das hipóteses do modelo.

### 9.6. Diagnóstico de leituras anormais

O microsserviço analisa padrões persistentes que podem indicar limitações de medição.

Dois diagnósticos principais são produzidos:

- **`persistent_zero`:** sinaliza uma sequência persistente de valores próximos de zero;
- **`sensor_saturated`:** sinaliza leituras persistentemente próximas do limite superior da escala.

A avaliação considera uma quantidade mínima de amostras consecutivas, uma tolerância para valores próximos de zero e uma margem de saturação.

Por padrão, os parâmetros de diagnóstico incluem cinco amostras consecutivas, tolerância de `0.5` para zero e margem de `1` para o limite superior.

Esses indicadores não comprovam defeito no hardware. Uma leitura persistentemente baixa ou alta pode corresponder à condição real do ambiente, mas também pode indicar falha, instalação inadequada ou necessidade de calibração. Por esse motivo, a identificação de qualquer um desses padrões leva à solicitação de validação manual.

### 9.7. Consulta à localização

A API utiliza o `userId` para consultar o registro correspondente no Supabase. Dentro desse registro, o microsserviço procura, na coluna `devices`, o dispositivo correspondente ao `devEUI`.

A estrutura de `devices` pode armazenar dispositivos como um objeto indexado por identificadores ou como uma lista.

O serviço aceita coordenadas nos campos:

- `lat` e `lon`; ou
- `latitude` e `longitude`.

As coordenadas são necessárias para consultar a previsão meteorológica adequada à localização associada ao sensor.

Caso o dispositivo não seja localizado ou as coordenadas não sejam válidas, a recomendação não pode ser calculada de forma confiável.

### 9.8. Consulta meteorológica ao Open-Meteo

Depois de obter as coordenadas do dispositivo, o microsserviço consulta o Open-Meteo para obter a previsão diária de seis dias.

As informações usadas incluem precipitação prevista, probabilidade máxima de precipitação e evapotranspiração de referência, quando disponível.

A soma da precipitação representa o volume total previsto no intervalo analisado. A probabilidade máxima representa a maior probabilidade diária fornecida pela previsão para aquele dia.

O algoritmo utiliza esses dados como indicadores para apoiar a decisão sobre o momento da irrigação. A previsão meteorológica permanece sujeita à incerteza inerente aos modelos meteorológicos.

### 9.9. Geração da recomendação

A recomendação combina o estado atual filtrado do solo, os diagnósticos do sensor e a previsão meteorológica.

O processamento segue esta lógica geral:

1. Verifica se há indícios que exijam validação manual do sensor.
2. Classifica a condição atual do solo com base nos limiares de umidade.
3. Se o solo estiver úmido, recomenda não irrigar naquele momento.
4. Se o solo estiver na faixa intermediária, solicita avaliação complementar.
5. Se o solo estiver seco, avalia a previsão meteorológica para identificar um dia mais adequado.
6. Se a chuva prevista for considerada relevante pelos limiares configurados, a recomendação incorpora uma condição de cautela.
7. Se as condições permitirem uma recomendação, retorna a ação, o dia sugerido e as respectivas mensagens explicativas.

Na escolha do dia de irrigação para um solo seco, a implementação utiliza uma pontuação baseada na probabilidade e no volume previsto de precipitação, com uma contribuição limitada do volume de chuva. A ideia é favorecer dias com menor indicação de chuva prevista, em vez de programar automaticamente a irrigação para qualquer dia do horizonte meteorológico.

A pontuação é uma heurística operacional. Ela não equivale a um modelo completo de demanda hídrica da cultura nem a uma otimização agronômica validada universalmente.

---

## 10. Interpretação das recomendações

O campo `irrigation` contém o resultado da decisão. A interpretação deve considerar a ação retornada, os indicadores booleanos e a mensagem explicativa.

### 10.1. Principais ações

| Ação | Significado |
|---|---|
| `manual_check_sensor` | Há um padrão nas leituras que exige inspeção ou validação manual do sensor. |
| `not_needed_now` | O solo foi classificado como úmido e a irrigação não é recomendada naquele momento. |
| `manual_check` | A umidade está em uma faixa que requer avaliação adicional, em vez de uma decisão automática. |
| `irrigate_with_caution` | O solo foi classificado como seco, mas a previsão indica condições de chuva que exigem cautela. |
| `irrigate_on_recommended_day` | O solo foi classificado como seco e o algoritmo identificou um dia recomendado de irrigação segundo os critérios implementados. |

Os nomes das ações são identificadores de máquina. O frontend pode exibir textos mais amigáveis, desde que preserve o significado da resposta.

### 10.2. Campos de decisão

| Campo | Interpretação |
|---|---|
| `should_irrigate` | Indica se a recomendação é irrigar; pode ser `null` quando não há decisão automática confiável. |
| `should_irrigate_today` | Indica se a irrigação é recomendada para o dia atual, conforme o resultado do algoritmo; pode ser `null` em casos que exigem validação manual. |
| `recommended_day` | Dia selecionado pelo algoritmo quando há uma recomendação para uma data específica. |
| `requires_manual_validation` | Indica se o resultado exige avaliação humana antes de ser utilizado como decisão operacional. |
| `soil_status` | Classificação do estado atual do solo. |
| `message` | Mensagem explicativa associada à recomendação. |

Um valor `null` não deve ser interpretado como `false`.

- `true` significa que o serviço indica a condição representada pelo campo.
- `false` significa que o serviço não recomenda essa condição.
- `null` significa que não foi produzida uma decisão booleana confiável para aquele campo, exigindo a análise de outros indicadores ou validação manual.

### 10.3. Quando o resultado exige cautela

A recomendação deve ser interpretada com atenção quando:

- o diagnóstico indicar leituras persistentemente nulas;
- o diagnóstico indicar possível saturação do sensor;
- a projeção apresentar alta incerteza;
- o solo estiver em uma faixa intermediária;
- a previsão meteorológica indicar chuva relevante próxima da data de irrigação;
- o sensor não tiver leituras recentes ou válidas suficientes.

O `Irrigacao-MSS` não deve transformar automaticamente uma estimativa incerta em um comando de acionamento de equipamento.

---

## 11. Tratamento e validação dos dados

O processamento depende da qualidade e da atualidade dos registros recebidos.

### 11.1. Validade e recência

São consideradas as condições de validade descritas na seção 9.2. Além disso, as leituras devem respeitar a janela temporal de até 24 horas e não podem estar mais de cinco minutos à frente do horário atual, conforme as verificações da implementação.

Quando os registros não atendem aos requisitos, o serviço não deve utilizá-los como base para a estimativa.

### 11.2. Identificação do dispositivo

Quando o registro da leitura contém `devEUI`, ele precisa corresponder ao dispositivo solicitado. Essa verificação ajuda a evitar que leituras de outro sensor sejam usadas por engano.

### 11.3. Consistência do timestamp

O timestamp é utilizado para ordenar as leituras e relacionar os dados ao período de previsão.

Uma alteração artificial dos timestamps pode facilitar testes, mas prejudica a rastreabilidade dos horários reais. A correção temporária mencionada anteriormente não deve substituir uma solução para a origem do problema de timestamp na integração.

### 11.4. Falhas em serviços externos

A recomendação depende do acesso à API de sensores, ao Supabase e ao Open-Meteo. Quando uma integração essencial falha ou retorna dados insuficientes, o microsserviço pode devolver um erro HTTP em vez de produzir uma recomendação incompleta ou sem suporte nos dados.

---

## 12. Códigos de resposta HTTP

Os códigos abaixo descrevem o comportamento esperado conforme as validações e o tratamento de erros implementados.

| Código HTTP | Significado |
|---|---|
| `200 OK` | A consulta foi processada e a resposta com os resultados foi gerada. |
| `400 Bad Request` | Faltam parâmetros obrigatórios ou há um parâmetro inválido, como um fuso horário não aceito. |
| `502 Bad Gateway` | Uma dependência externa necessária retornou erro ou não forneceu os dados válidos esperados. |

Os erros são retornados em JSON, com uma descrição do problema. Por exemplo:

```json
{
  "error": "Descrição do erro"
}
```

O texto exato depende da falha encontrada. Não é recomendável que o frontend dependa de uma mensagem específica para tomar decisões; para isso, é preferível usar o status HTTP e os campos estruturados da resposta.

O endpoint `/healthz` é usado para verificar a disponibilidade do processo, mas não substitui a verificação das dependências externas.

---

## 13. Limitações e considerações

### 13.1. Dependência da qualidade do sensor

O filtro de Kalman reduz o efeito de ruídos, mas não corrige automaticamente defeitos físicos, problemas de instalação, deriva de calibração ou erros sistemáticos.

### 13.2. Limiares configuráveis

Os limiares de solo seco e úmido utilizados como padrão são referências operacionais. A interpretação adequada da umidade depende do sensor, da escala de saída, do solo, da profundidade de instalação e da cultura.

Antes de utilizar as recomendações em uma aplicação real, esses valores devem ser calibrados e validados para as condições desejadas.

### 13.3. Horizonte da projeção

A projeção da umidade considera o histórico filtrado e uma estimativa de tendência. Ela não deve ser interpretada como uma simulação física completa da dinâmica da água no solo.

Fatores como tipo de solo, profundidade radicular, drenagem, evapotranspiração da cultura e quantidade efetiva de água aplicada podem alterar a evolução real da umidade.

### 13.4. Incerteza meteorológica

A previsão de precipitação e evapotranspiração está sujeita às incertezas do serviço meteorológico. A chuva prevista não garante que o volume esperado ocorrerá exatamente no local do sensor.

### 13.5. Ausência de acionamento automático

O serviço retorna recomendações em JSON, sem acionar diretamente os atuadores. A automação física exigiria uma etapa adicional de integração, controles de segurança, validação das decisões e definição explícita das condições sob as quais um equipamento poderia ser acionado.

### 13.6. Uso responsável

As recomendações devem ser tratadas como informações de apoio à decisão. Para uso operacional, é importante validar os limiares, observar o comportamento real do sensor e comparar as estimativas com medições independentes ou avaliações agronômicas.

---

## 14. Solução de problemas

### 14.1. Erro de leitura válida ou recente

**Sintoma:** a API retorna um erro informando que não encontrou uma leitura válida e recente.

Verifique:

1. se o `userId` está correto;
2. se o `devEUI` corresponde ao dispositivo consultado;
3. se a API de sensores está retornando os registros esperados;
4. se os registros possuem `validity: true`;
5. se o timestamp é positivo e recente;
6. se o campo `soil_moisture` está presente e é numérico;
7. se o timestamp está sendo modificado incorretamente por alguma etapa da integração.

É útil consultar diretamente o endpoint da API de sensores para verificar o JSON retornado antes de investigar o filtro.

### 14.2. Dispositivo não encontrado no Supabase

**Sintoma:** a leitura existe, mas o serviço não consegue localizar a posição do dispositivo.

Verifique se:

- `SUPABASE_URL` corresponde ao projeto correto;
- `SUPABASE_KEY` tem as permissões necessárias;
- `SUPABASE_TABLE` aponta para a tabela correta;
- `SUPABASE_USER_COLUMN` corresponde à coluna de identificação do usuário;
- `SUPABASE_DEVICES_COLUMN` corresponde à coluna que armazena os dispositivos;
- o `userId` da requisição corresponde ao registro do banco;
- o JSON de `devices` contém o `devEUI` informado;
- o dispositivo possui coordenadas válidas.

Na configuração padrão, o serviço consulta a tabela `users`, utiliza a coluna `userId` para localizar o usuário e lê os dispositivos da coluna `devices`.

### 14.3. Erro ao consultar o Open-Meteo

Verifique:

- se as coordenadas estão presentes e dentro dos intervalos geográficos válidos;
- se o ambiente possui acesso à internet;
- se o serviço meteorológico está acessível;
- se a resposta contém os seis dias de previsão necessários;
- se os dados de precipitação e probabilidade retornados possuem a estrutura esperada.

A ET₀ pode não estar disponível sem que todos os outros dados meteorológicos estejam necessariamente ausentes.

### 14.4. Projeção com alta incerteza

Uma projeção com `confidence_status: high_uncertainty` indica que o intervalo de valores possíveis alcançou uma largura considerada elevada pelo parâmetro configurado.

Isso pode ocorrer quando a incerteza aumenta durante a projeção ou quando o histórico disponível não permite restringir adequadamente os valores futuros.

Não significa necessariamente que o sensor esteja com defeito. A projeção deve ser interpretada em conjunto com o histórico, a tendência, os diagnósticos e as condições meteorológicas.

### 14.5. Recomendação de validação manual

Quando `requires_manual_validation` for `true`, ou quando os campos de decisão estiverem como `null`, o resultado não deve ser convertido automaticamente em uma ordem de irrigação.

Consulte a mensagem retornada, os diagnósticos e o estado do solo para entender o motivo da validação requerida.

### 14.6. Verificação da disponibilidade

Teste o endpoint de saúde:

```bash
curl http://localhost:8456/healthz
```

Em seguida, teste a consulta de irrigação diretamente, utilizando os identificadores reais e verificando o status HTTP:

```bash
curl -i -G "http://localhost:8456/api/v1/irrigation" \
  --data-urlencode "userId=SEU_USER_ID" \
  --data-urlencode "devEUI=SEU_DEV_EUI" \
  --data-urlencode "timezone=America/Sao_Paulo"
```

O uso de `-i` permite visualizar os cabeçalhos HTTP e o código de status da resposta.

---

## 15. Descrição metodológica para artigo científico

O microsserviço proposto, denominado `Irrigacao-MSS`, foi desenvolvido em Go com o objetivo de integrar dados de sensores de umidade do solo e informações meteorológicas para produzir recomendações de irrigação. O processamento começa pela recuperação das leituras recentes associadas ao dispositivo monitorado, seguida da validação dos registros segundo critérios de integridade, identificação, recência e disponibilidade do valor de umidade. As medições válidas são ordenadas cronologicamente e submetidas a um filtro de Kalman unidimensional para suavização da série temporal. Em paralelo, a localização geográfica associada ao dispositivo é recuperada do Supabase e utilizada na consulta à previsão meteorológica do Open-Meteo, que fornece informações de precipitação, probabilidade de chuva e evapotranspiração de referência, quando disponível.

Para estimar o comportamento futuro da umidade, o serviço utiliza um modelo de Kalman com componentes de nível e tendência, produzindo uma projeção para as 24 horas seguintes e calculando um intervalo associado à incerteza da estimativa. A decisão é baseada principalmente no valor de umidade filtrado, em limiares configuráveis de classificação do solo e nas condições meteorológicas previstas para os seis dias analisados. Leituras que apresentam padrões persistentes próximos de zero ou do limite superior da escala geram indicadores de diagnóstico e podem exigir validação manual. Quando o solo é classificado como seco, os dados meteorológicos contribuem para selecionar um dia mais adequado à irrigação segundo uma regra heurística que considera a probabilidade e o volume de precipitação. Dessa forma, o microsserviço consolida diferentes fontes de dados em uma resposta estruturada, destinada a apoiar a decisão do usuário.

Embora o método permita integrar filtragem de medições, estimativa de tendência e previsão meteorológica em um único serviço, os resultados dependem da qualidade dos sensores, da calibração dos limiares e da incerteza dos dados meteorológicos. A projeção de umidade utilizada não equivale a um modelo físico completo do balanço hídrico do solo, e a recomendação não substitui a validação experimental ou agronômica. Portanto, a solução deve ser entendida como um mecanismo computacional de apoio à decisão, cuja eficácia deve ser avaliada em condições reais por meio da comparação entre as recomendações produzidas, as condições observadas do solo e os resultados de irrigação.