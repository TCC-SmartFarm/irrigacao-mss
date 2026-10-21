# Irrigacao-MSS

Microsserviço de recomendação de irrigação desenvolvido em Go. O serviço integra dados de sensores de umidade do solo, informações geográficas armazenadas no Supabase e previsões meteorológicas da Open-Meteo para avaliar as condições do solo e identificar um momento potencialmente mais adequado para a irrigação.

O sistema combina processamento de séries temporais, filtragem de ruído por meio do filtro de Kalman e análise da previsão de precipitação em uma janela de seis dias. Seu objetivo é auxiliar a tomada de decisão sobre a irrigação, reduzindo a influência de oscilações nas medições e considerando a possibilidade de chuva nos dias seguintes.

> **Importante:** este microsserviço gera recomendações. Ele não aciona diretamente equipamentos de irrigação, e suas recomendações devem ser validadas considerando a cultura, o solo e as condições reais do local.

---

## 1. Visão geral da arquitetura

O processamento envolve três fontes principais de dados:

1. **API de sensores:** fornece as leituras de umidade do solo, umidade do ar, temperatura do ar, timestamp e indicador de validade.
2. **Supabase:** armazena as coordenadas geográficas associadas ao dispositivo.
3. **Open-Meteo:** fornece a previsão diária de precipitação e, quando disponível, a evapotranspiração de referência (ET₀).

A partir dessas informações, o microsserviço processa as leituras, consulta a previsão meteorológica e produz uma resposta estruturada em JSON.

### Fluxo de execução

```text
Requisição HTTP
      |
      v
Identificação do usuário e sensor
      |
      v
Consulta à API de sensores
      |
      v
Validação das leituras
      |
      v
Ordenação temporal das medições
      |
      v
Filtro de Kalman
      |
      v
Consulta das coordenadas no Supabase
      |
      v
Previsão meteorológica da Open-Meteo
      |
      v
Classificação da umidade do solo
      |
      v
Análise dos seis dias de previsão
      |
      v
Resposta JSON com recomendação
```

## 2. Requisitos

- Go 1.22 ou superior.
- Acesso à API que disponibiliza os dados dos sensores.
- Projeto Supabase com uma tabela contendo os dispositivos e suas coordenadas.
- Conexão com a internet para consultar a Open-Meteo.

## 3. Configuração e execução

### 3.1. Variáveis de ambiente

Copie o arquivo de exemplo e configure os valores necessários:

```bash
cp .env.example .env
```

Edite o arquivo `.env`:

```dotenv
PORT=8456

SENSOR_API_URL_TEMPLATE=http://localhost:3000/api/sensors/{user}/{devEUI}

SUPABASE_URL=https://SEU_PROJETO.supabase.co
SUPABASE_KEY=SUA_CHAVE_SUPABASE

SUPABASE_TABLE=profiles
SUPABASE_USER_COLUMN=id
SUPABASE_DEVICES_COLUMN=devices

SOIL_DRY_THRESHOLD=30
SOIL_WET_THRESHOLD=70

RAIN_PROBABILITY_THRESHOLD=60
MIN_RAIN_MM=3

KALMAN_PROCESS_NOISE=1
KALMAN_MEASUREMENT_NOISE=4

IRRIGATION_TIMEZONE=America/Sao_Paulo
```

O endereço da API de sensores acima é apenas um exemplo. Substitua-o pela URL real do serviço utilizado.

As variáveis têm as seguintes funções:

| Variável | Descrição |
|---|---|
| `PORT` | Porta HTTP do microsserviço. Padrão: `8456`. |
| `SENSOR_API_URL_TEMPLATE` | URL da API de sensores, contendo `{user}` e `{devEUI}`. |
| `SUPABASE_URL` | URL do projeto Supabase. |
| `SUPABASE_KEY` | Chave utilizada para acessar a API do Supabase. |
| `SUPABASE_TABLE` | Tabela que contém as coordenadas dos dispositivos. Padrão: `profiles`. |
| `SUPABASE_USER_COLUMN` | Coluna utilizada para identificar o usuário. Padrão: `id`. |
| `SUPABASE_DEVICES_COLUMN` | Coluna JSON que contém os dispositivos. Padrão: `devices`. |
| `SOIL_DRY_THRESHOLD` | Limite máximo para classificar o solo como seco. |
| `SOIL_WET_THRESHOLD` | Limite mínimo para classificar o solo como úmido. |
| `RAIN_PROBABILITY_THRESHOLD` | Limite percentual utilizado na interpretação da previsão de chuva. |
| `MIN_RAIN_MM` | Volume mínimo de chuva considerado relevante na regra de recomendação. |
| `KALMAN_PROCESS_NOISE` | Parâmetro de ruído do processo do filtro de Kalman. |
| `KALMAN_MEASUREMENT_NOISE` | Parâmetro de ruído da medição do filtro de Kalman. |
| `IRRIGATION_TIMEZONE` | Fuso horário padrão utilizado para interpretar os dias da previsão. |

Os valores numéricos apresentados são configurações iniciais para testes, não parâmetros universais para todas as culturas e tipos de solo.

### 3.2. Iniciar o serviço

```bash
set -a
source .env
set +a

go test ./...
go run .
```

O serviço ficará disponível, por padrão, em `http://localhost:8456`.

### 3.3. Verificar a disponibilidade

**Endpoint:** `GET /healthz`

```bash
curl -sS http://localhost:8456/healthz
```

Resposta esperada:

```json
{
  "status": "ok"
}
```

Esse endpoint verifica se o serviço HTTP está respondendo. Ele não garante que a API de sensores, o Supabase ou a Open-Meteo estejam disponíveis.

---

## 4. Utilização da API de irrigação

### 4.1. Endpoint principal

**Método:** `GET`

**Rota:** `/api/v1/irrigation`

**Parâmetros de consulta:**

| Parâmetro | Obrigatório | Descrição |
|---|---|---|
| `userId` | Sim | Identificador do usuário associado ao sensor. |
| `devEUI` | Sim | Identificador único do dispositivo. |
| `timezone` | Não | Fuso horário utilizado no processamento das datas. Padrão: `America/Sao_Paulo`. |

### 4.2. Exemplo de requisição

```bash
curl -sS -G "http://localhost:8456/api/v1/irrigation" \
  --data-urlencode "userId=ID_DO_USUARIO" \
  --data-urlencode "devEUI=5e76ce4fd99eefe3" \
  --data-urlencode "timezone=America/Sao_Paulo"
```

Também é possível acessar a URL diretamente pelo navegador:

```text
http://localhost:8456/api/v1/irrigation?userId=ID_DO_USUARIO&devEUI=5e76ce4fd99eefe3&timezone=America%2FSao_Paulo
```

O parâmetro `userId` deve corresponder ao identificador armazenado na coluna configurada em `SUPABASE_USER_COLUMN`. O serviço utiliza esse identificador para localizar o registro do usuário e, posteriormente, encontrar o dispositivo solicitado.

### 4.3. Formato esperado pela API de sensores

O microsserviço aceita uma resposta contendo um único sensor ou uma lista de sensores. Cada registro deve ser compatível com a seguinte estrutura:

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

O timestamp deve representar o horário Unix em segundos. O campo `soil_moisture` deve ser numérico e estar presente para que a leitura seja utilizada. O campo `validity` deve ser `true`.

Por padrão, o microsserviço considera leituras com timestamp válido, que não estejam mais de cinco minutos no futuro e cuja idade não ultrapasse 24 horas. Leituras que não atendam aos critérios são descartadas.

Quando a API retorna várias leituras válidas, elas são ordenadas cronologicamente e utilizadas para calcular a estimativa filtrada da umidade do solo.

### 4.4. Localização do dispositivo no Supabase

O microsserviço não recebe latitude e longitude na requisição HTTP. As coordenadas são obtidas da coluna JSON definida por `SUPABASE_DEVICES_COLUMN`.

Exemplo de estrutura da coluna `devices`:

```json
{
  "1": {
    "devEUI": "5e76ce4fd99eefe3",
    "lat": -23.6471338,
    "lon": -46.5741899
  }
}
```

Também são aceitos os campos `latitude` e `longitude` em substituição a `lat` e `lon`.

O serviço procura o dispositivo pelo `devEUI` informado. Caso não encontre o usuário, o dispositivo ou coordenadas válidas, o processamento é interrompido e um erro é retornado.

---

## 5. Interpretação da resposta JSON

A resposta de sucesso contém cinco componentes principais: identificação do sensor, localização, dados processados do sensor, previsão meteorológica e recomendação de irrigação.

### 5.1. Exemplo ilustrativo

Os valores abaixo são fictícios e servem apenas para demonstrar a estrutura da resposta.

```json
{
  "sensor_id": "5e76ce4fd99eefe3",
  "location": {
    "latitude": -23.647134,
    "longitude": -46.574190,
    "timezone": "America/Sao_Paulo",
    "source": "Supabase"
  },
  "sensor": {
    "timestamp": "2026-10-21T15:00:00Z",
    "soil_moisture_raw": 28.0,
    "soil_moisture_kalman_filtered": 29.4,
    "air_humidity_percent": 60.0,
    "air_temperature_celsius": 25.5,
    "validity": true
  },
  "forecast_today_and_next_5_days": [
    {
      "date": "2026-10-21",
      "precipitation_probability_percent": 20,
      "precipitation_mm": 0.5,
      "et0_mm": 4.2,
      "rain_likely": false
    }
  ],
  "irrigation": {
    "soil_status": "dry",
    "action": "irrigate_on_recommended_day",
    "should_irrigate": true,
    "should_irrigate_today": true,
    "recommended_day": "2026-10-21",
    "rain_probability_percent": 20,
    "expected_rain_mm": 0.5,
    "message": "Exemplo de mensagem explicativa."
  },
  "note": "Observações sobre a filtragem e as limitações da recomendação."
}
```

O exemplo apresenta apenas um elemento da previsão para facilitar a leitura. Na resposta real, o campo `forecast_today_and_next_5_days` contém até seis dias, incluindo hoje e os cinco dias seguintes.

**Observação:** os resultados são ilustrativos; o texto de `message` e os demais campos são determinados pelo processamento efetivo do serviço.

### 5.2. Campos de identificação e localização

| Campo | Interpretação |
|---|---|
| `sensor_id` | Identificador do sensor consultado. |
| `location.latitude` | Latitude associada ao dispositivo. |
| `location.longitude` | Longitude associada ao dispositivo. |
| `location.timezone` | Fuso horário utilizado para a interpretação das datas. |
| `location.source` | Origem dos dados de localização. |

### 5.3. Campos do sensor

| Campo | Interpretação |
|---|---|
| `sensor.timestamp` | Timestamp da leitura selecionada, convertido para uma data em UTC. |
| `sensor.soil_moisture_raw` | Valor original da umidade do solo recebido da API. |
| `sensor.soil_moisture_kalman_filtered` | Estimativa da umidade do solo após a aplicação do filtro de Kalman. |
| `sensor.air_humidity_percent` | Umidade relativa do ar fornecida pelo sensor. |
| `sensor.air_temperature_celsius` | Temperatura do ar fornecida pelo sensor, em graus Celsius. |
| `sensor.validity` | Indicador de validade da leitura selecionada. |

A umidade bruta e a filtrada não representam necessariamente o mesmo valor. A filtrada procura reduzir oscilações entre medições consecutivas, mas não substitui a calibração do sensor nem garante que o valor estimado corresponda à umidade volumétrica real do solo.

### 5.4. Campos da previsão meteorológica

| Campo | Interpretação |
|---|---|
| `date` | Data local do dia previsto, no fuso horário solicitado. |
| `precipitation_probability_percent` | Probabilidade máxima diária de precipitação fornecida pela Open-Meteo, em porcentagem. |
| `precipitation_mm` | Volume total de precipitação previsto para o dia, em milímetros. |
| `et0_mm` | Evapotranspiração de referência estimada para o dia, em milímetros, quando disponível. |
| `rain_likely` | Indica se a probabilidade diária atingiu ou ultrapassou `RAIN_PROBABILITY_THRESHOLD`. |

A probabilidade de precipitação e o volume previsto são grandezas diferentes. Uma probabilidade elevada não significa necessariamente que haverá um grande volume de chuva, assim como um volume previsto pode ser relevante mesmo com uma probabilidade inferior ao limite configurado.

A ET₀ representa uma estimativa da demanda evaporativa de referência da atmosfera. Ela não equivale diretamente à necessidade hídrica de uma cultura específica.

---

## 6. Como o algoritmo calcula a recomendação

O algoritmo foi desenvolvido em etapas independentes, permitindo separar o tratamento dos dados medidos, a interpretação das condições do solo e a análise da previsão meteorológica.

### 6.1. Validação e ordenação das leituras

Primeiro, o serviço obtém os registros da API de sensores e verifica os seguintes critérios:

- O `devEUI`, quando presente, deve corresponder ao dispositivo solicitado.
- O timestamp deve ser positivo e estar dentro da janela de tempo permitida.
- A leitura deve estar marcada como válida.
- A umidade do solo deve estar presente.

Depois da validação, as leituras são ordenadas pelo timestamp em ordem crescente. Isso é necessário porque o filtro de Kalman processa uma sequência temporal, e não um conjunto de medições em ordem arbitrária.

Se nenhuma leitura for aceita, a API retorna um erro, pois não há dados suficientes para calcular uma recomendação.

### 6.2. Filtragem da umidade do solo pelo filtro de Kalman

Sensores podem apresentar pequenas oscilações devido a ruídos eletrônicos, limitações de precisão, condições de instalação ou variações locais. Para reduzir o efeito dessas oscilações, o microsserviço aplica um filtro de Kalman unidimensional à sequência de umidade do solo.

O algoritmo mantém duas grandezas internas:

- \(x_k\): estimativa filtrada da umidade no instante \(k\).
- \(P_k\): covariância estimada do erro da estimativa.

A cada nova medição \(z_k\), o filtro executa uma etapa de predição e outra de correção.

**Predição da covariância:**

\[
P_k^- = P_{k-1} + Q
\]

**Ganho de Kalman:**

\[
K_k = \frac{P_k^-}{P_k^- + R}
\]

**Atualização da estimativa:**

\[
x_k = x_{k-1} + K_k(z_k-x_{k-1})
\]

**Atualização da covariância:**

\[
P_k = (1-K_k)P_k^-
\]

Em que:

- \(z_k\) é a medição bruta da umidade do solo;
- \(Q\) é o parâmetro de ruído do processo;
- \(R\) é o parâmetro de ruído da medição;
- \(K_k\) determina o peso atribuído à nova medição em relação à estimativa anterior.

Na implementação atual, a primeira estimativa é inicializada com a primeira medição válida, e a covariância inicial é definida por \(R\).

O filtro é aplicado à sequência de leituras de umidade do solo, não às variáveis meteorológicas. A estimativa final da sequência é utilizada na classificação do estado do solo.

**Efeito dos parâmetros:** valores maiores de \(R\), mantendo \(Q\) constante, tendem a aumentar a suavização das medições. Valores maiores de \(Q\), mantendo \(R\) constante, permitem que a estimativa se adapte mais rapidamente às mudanças observadas.

Os parâmetros devem ser calibrados com dados reais. Como as medições são utilizadas na escala numérica original do sensor, \(Q\) e \(R\) devem ser interpretados em relação à escala e às unidades dessas medições.

### 6.3. Classificação da condição do solo

Após a filtragem, o algoritmo compara a estimativa final com dois limites configuráveis:

- `SOIL_DRY_THRESHOLD`: limite de classificação como solo seco.
- `SOIL_WET_THRESHOLD`: limite de classificação como solo úmido.

A classificação é definida pelas seguintes regras:

| Condição | Regra | `soil_status` | Interpretação |
|---|---|---|---|
| Solo seco | \(x \leq D\) | `dry` | A umidade está abaixo ou no limite seco. |
| Faixa intermediária | \(D < x < W\) | `intermediate` | A leitura não é suficiente para uma decisão automática. |
| Solo úmido | \(x \geq W\) | `wet` | A umidade está acima ou no limite úmido. |

Em que \(x\) é a estimativa filtrada, \(D\) é o limite seco e \(W\) é o limite úmido.

Com os parâmetros padrão, \(D=30\) e \(W=70\). Esses números são exemplos e só fazem sentido quando compatíveis com a escala efetivamente utilizada pelo sensor.

O comportamento de cada classe é:

- **`dry`:** o algoritmo avalia a previsão meteorológica para selecionar um dia recomendado.
- **`intermediate`:** o algoritmo recomenda uma verificação manual, pois não há evidência suficiente para classificar o solo como seco ou úmido.
- **`wet`:** o algoritmo recomenda não irrigar naquele momento.

### 6.4. Consulta e análise da previsão meteorológica

O serviço consulta a Open-Meteo utilizando as coordenadas geográficas do sensor e o fuso horário configurado. A previsão solicitada abrange seis dias: o dia atual e os cinco seguintes.

Para cada dia, são utilizados:

- Probabilidade máxima diária de precipitação.
- Volume diário de precipitação previsto, em milímetros.
- ET₀ diária, quando disponibilizada pela resposta.

O algoritmo não calcula uma nova probabilidade de chuva a partir das leituras do sensor. Ele utiliza a previsão meteorológica fornecida pela Open-Meteo.

Essa separação é importante porque a umidade do solo e a probabilidade de precipitação são variáveis distintas. Além disso, as probabilidades referentes a dias diferentes descrevem eventos meteorológicos diferentes; filtrá-las como se fossem medições sucessivas da mesma variável não seria adequado sem um modelo temporal específico.

### 6.5. Seleção do dia recomendado para irrigação

Quando o solo é classificado como seco, o algoritmo atribui uma pontuação a cada um dos seis dias previstos. A pontuação combina a probabilidade de chuva com o volume de precipitação esperado.

A expressão utilizada é:

\[
S_i = P_i + \min(3R_i,30)
\]

Em que:

- \(S_i\) é a pontuação do dia \(i\);
- \(P_i\) é a probabilidade máxima diária de precipitação, em pontos percentuais;
- \(R_i\) é o volume previsto de precipitação, em milímetros;
- \(\min(3R_i,30)\) limita a contribuição do volume de chuva a 30 pontos.

O volume previsto recebe um peso de três pontos por milímetro, até atingir o limite de 30 pontos. Portanto, o componente de probabilidade pode contribuir com até 100 pontos, enquanto o componente de volume contribui com até 30.

O dia recomendado é aquele que apresenta a menor pontuação:

\[
i^* = \underset{i}{\arg\min}\; S_i
\]

Esse critério favorece dias com menor risco combinado de precipitação, conforme os pesos definidos na implementação. Ele é uma heurística de decisão, e não uma previsão probabilística desenvolvida especificamente para a necessidade hídrica da cultura.

Se dois dias apresentarem a mesma pontuação mínima, a implementação atual seleciona o primeiro deles na ordem cronológica.

### 6.6. Interpretação final da recomendação

Após a seleção do dia, o microsserviço compara a previsão com dois parâmetros adicionais:

- `RAIN_PROBABILITY_THRESHOLD`: limite para considerar elevada a probabilidade de chuva.
- `MIN_RAIN_MM`: volume mínimo para considerar a precipitação relevante.

Se a probabilidade prevista for maior ou igual ao limite e o volume esperado também for maior ou igual ao mínimo, a resposta indica cautela na irrigação. Caso contrário, o algoritmo apresenta a recomendação de irrigar no dia selecionado.

O campo `should_irrigate_today` indica se a data recomendada coincide com a data atual no fuso horário escolhido. Ele não significa, isoladamente, que a irrigação deva ser acionada sem validação.

O campo `should_irrigate` representa a indicação geral derivada da classificação do solo. Quando o solo está seco, o algoritmo indica que a irrigação é necessária; quando está úmido, indica que não é necessária naquele momento. Na faixa intermediária, a decisão permanece indeterminada e requer verificação.

O campo `action` identifica a ação sugerida pelo algoritmo, enquanto `message` apresenta uma explicação textual da recomendação.

---

## 7. Parâmetros de calibração

A qualidade das recomendações depende da calibração dos limiares e dos parâmetros do filtro.

| Parâmetro | Efeito | Orientação |
|---|---|---|
| `SOIL_DRY_THRESHOLD` | Define quando o solo passa a ser classificado como seco. | Calibrar com medições e referências agronômicas. |
| `SOIL_WET_THRESHOLD` | Define quando o solo passa a ser classificado como úmido. | Considerar a faixa adequada para a cultura e o tipo de solo. |
| `RAIN_PROBABILITY_THRESHOLD` | Define o limite para classificar a probabilidade de chuva como elevada. | Ajustar à tolerância ao risco e às condições locais. |
| `MIN_RAIN_MM` | Define o volume mínimo para tratar a chuva prevista como relevante. | Considerar a capacidade de infiltração e a necessidade hídrica. |
| `KALMAN_PROCESS_NOISE` | Controla a adaptação da estimativa a mudanças na sequência. | Avaliar a resposta a variações reais da umidade. |
| `KALMAN_MEASUREMENT_NOISE` | Controla o peso atribuído às novas medições. | Comparar as leituras brutas e filtradas. |

Os valores de ruído do filtro correspondem a variâncias na escala da medição: se a umidade é representada em uma escala percentual, suas variâncias são expressas na escala percentual ao quadrado.

Os limiares de umidade não devem ser considerados universais. Uma mesma leitura pode ter interpretações diferentes conforme o sensor, o substrato, o tipo de solo e a cultura cultivada.

A ET₀ está presente na resposta para fornecer contexto sobre a demanda evaporativa, mas **não participa diretamente da pontuação utilizada para selecionar o dia recomendado na versão atual do algoritmo**.

---

## 8. Tratamento de erros

A API utiliza códigos HTTP para distinguir respostas bem-sucedidas de falhas durante o processamento.

| Código HTTP | Significado |
|---|---|
| `200 OK` | Requisição processada e resultado retornado. |
| `400 Bad Request` | Parâmetros obrigatórios ausentes ou fuso horário inválido. |
| `502 Bad Gateway` | Falha na obtenção ou validação dos dados externos necessários ao processamento. |

Exemplos de mensagens de erro:

```json
{
  "error": "informe userId e devEUI na query string"
}
```

```json
{
  "error": "API não retornou leitura válida e recente para esse sensor"
}
```

```json
{
  "error": "Supabase: falha ao consultar os dados do dispositivo"
}
```

A última mensagem é ilustrativa; o texto real depende da falha retornada pelo serviço externo.

Um erro `502` não significa necessariamente que a lógica de recomendação está incorreta. Ele pode ocorrer por indisponibilidade de uma API externa, ausência de leituras válidas, falha de autenticação ou dados de localização inválidos.

---

## 9. Limitações e melhorias futuras

A implementação atual utiliza uma regra de decisão relativamente simples, com o objetivo de manter o algoritmo compreensível e facilitar sua validação experimental.

Entre as limitações atuais estão:

- Os limiares de umidade precisam ser calibrados para o sensor e a cultura.
- A filtragem de Kalman reduz oscilações, mas não corrige erros sistemáticos de calibração.
- A previsão meteorológica está sujeita às incertezas do modelo e à resolução espacial dos dados.
- A pontuação diária utiliza pesos fixos para probabilidade e volume de precipitação.
- A ET₀ é retornada, mas ainda não participa do cálculo da pontuação nem de um balanço hídrico.
- O algoritmo não calcula a lâmina de irrigação, o tempo de acionamento ou o volume de água necessário.
- O serviço não comanda bombas, válvulas ou outros atuadores.

Como evolução, podem ser incorporados modelos de balanço hídrico, parâmetros específicos por cultura e solo, calibração experimental dos pesos, análise histórica de precipitação e avaliação quantitativa da qualidade das recomendações.

Antes de utilizar o sistema em operação real, é necessário comparar as recomendações com medições de campo e decisões agronômicas de referência.

Também é importante manter as chaves do Supabase protegidas no servidor e implementar autenticação e autorização para o endpoint antes de disponibilizá-lo em um ambiente multiusuário.

---

## 10. Texto técnico para artigo científico

### Desenvolvimento de um microsserviço para apoio à decisão de irrigação

Foi desenvolvido um microsserviço em linguagem Go para apoiar a tomada de decisão relacionada à irrigação, integrando dados de sensores de campo, informações geográficas e previsões meteorológicas. A solução foi estruturada como uma API HTTP, permitindo que outros componentes de uma arquitetura de monitoramento agrícola solicitem uma recomendação por meio da identificação do usuário e do dispositivo sensor.

O processamento inicia-se pela obtenção e validação das leituras disponibilizadas pela API de sensores. São consideradas a validade declarada da medição, a presença do valor de umidade do solo e a consistência temporal do registro. Quando múltiplas leituras válidas estão disponíveis, elas são ordenadas cronologicamente e submetidas a um filtro de Kalman unidimensional, empregado para reduzir a influência de oscilações nas medições consecutivas. A estimativa resultante é então comparada com limiares configuráveis, possibilitando classificar a condição do solo como seca, intermediária ou úmida.

Em paralelo, as coordenadas geográficas associadas ao dispositivo são recuperadas do banco de dados Supabase e utilizadas para consultar a previsão meteorológica da Open-Meteo. São obtidos dados diários de probabilidade máxima de precipitação, volume previsto de chuva e evapotranspiração de referência, quando disponível, para uma janela de seis dias. Quando a umidade filtrada caracteriza uma condição seca, o algoritmo calcula uma pontuação para cada dia da previsão, combinando a probabilidade de precipitação e o volume esperado de chuva. O dia de menor pontuação é selecionado como candidato à irrigação, sendo posteriormente avaliado por limites configuráveis de probabilidade e volume de precipitação para qualificar a recomendação.

A arquitetura separa o tratamento das medições do sensor da interpretação dos dados meteorológicos. Essa separação evita que probabilidades referentes a dias distintos sejam tratadas como medições sucessivas de uma mesma variável, preservando a interpretação da previsão fornecida pelo serviço meteorológico. Além disso, a disponibilização dos resultados em formato JSON favorece a integração com interfaces de monitoramento e outros microsserviços.

A solução constitui uma abordagem modular de apoio à decisão, na qual a filtragem das medições e a análise meteorológica são combinadas por regras explícitas e parâmetros ajustáveis. Entretanto, a recomendação obtida representa uma indicação computacional preliminar, e não uma determinação universal da necessidade hídrica da cultura. A avaliação de sua eficácia depende da calibração dos limiares, da qualidade dos sensores e da comparação dos resultados com observações e critérios agronômicos de referência. Como possibilidades de evolução, destacam-se a incorporação da evapotranspiração ao cálculo da demanda hídrica, o desenvolvimento de um balanço hídrico do solo e a validação experimental dos parâmetros de decisão em diferentes condições de cultivo.