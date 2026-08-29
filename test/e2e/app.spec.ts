import { expect, test } from '@playwright/test';

test('configures providers, creates a client key, and routes an OpenAI request', async ({ page, request }) => {
  await request.post('http://127.0.0.1:19475/__mock/scenario', {
    data: {
      models: [{ id: 'model-a:free', name: 'Model A Free', prompt_price: '0', completion_price: '0', context_length: 128000, max_completion_tokens: 4096, supported_parameters: ['tools', 'response_format'], input_modalities: ['text', 'image'], output_modalities: ['text'], supported_features: ['streaming', 'free-tier'] }],
      response_text: 'mock response',
      failure_count: 1,
    },
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'PayLessForAI' })).toBeVisible();
  const emptyStatus = await (await request.get('/api/status')).json();
  expect(emptyStatus.model_count).toBe(0);
  const providerDefinitions = await (await request.get('/api/providers')).json();
  expect(providerDefinitions.data.map((item: { name: string }) => item.name)).toEqual(expect.arrayContaining(['openrouter', 'surplus']));

  await page.getByRole('link', { name: 'Access & keys' }).click();
  await page.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.locator('#provider-type')).toHaveValue('openrouter');
  await expect(page.locator('#custom-provider-fields')).toBeHidden();
  await page.locator('#provider-type').selectOption('custom');
  await expect(page.locator('#custom-provider-fields')).toBeVisible();
  await expect(page.locator('#provider-name')).toHaveAttribute('required', '');
  await expect(page.locator('#provider-base-url')).toHaveAttribute('required', '');
  await page.locator('#provider-type').selectOption('openrouter');
  await expect(page.locator('#custom-provider-fields')).toBeHidden();
  await page.locator('#provider-label').fill('mock-openrouter');
  await page.locator('#provider-key').fill('mock-key');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-feedback')).toContainText('Found 1 model');
  await expect(page.locator('#provider-list')).toContainText('openrouter');
  await expect(page.locator('#provider-modal')).toBeHidden();

  await page.getByRole('button', { name: 'Add provider' }).click();
  await page.locator('#provider-type').selectOption('openrouter');
  await page.locator('#provider-label').fill('duplicate-openrouter');
  await page.locator('#provider-key').fill('mock-key');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-feedback')).toContainText('already configured');
  await page.getByRole('button', { name: 'Cancel' }).click();

  await page.getByRole('button', { name: 'Add provider' }).click();
  await page.locator('#provider-type').selectOption('surplus');
  await page.locator('#provider-label').fill('mock-surplus');
  await page.locator('#provider-key').fill('surplus-key');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-feedback')).toContainText('Found 1 model');
  await expect(page.locator('#provider-list')).toContainText('surplus');

  await page.getByRole('button', { name: 'Create API key' }).click();
  await page.locator('#key-label').fill('playwright');
  await page.locator('#key-modal').getByRole('button', { name: 'Create key' }).click();
  await expect(page.locator('#new-key')).toContainText('plai_');
  const secretText = await page.locator('#new-key').textContent();
  const secret = secretText?.match(/plai_[0-9a-f]+/)?.[0];
  expect(secret).toBeTruthy();
  await page.locator('#key-modal').getByRole('button', { name: 'Close' }).click();

  const response = await request.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model: 'model-a', messages: [{ role: 'user', content: 'hello' }] },
  });
  expect(response.ok()).toBeTruthy();
  expect((await response.json()).choices[0].message.content).toBe('mock response');
  const openRouterRequests = await (await request.get('http://127.0.0.1:19475/__mock/requests')).json();
  const surplusRequests = await (await request.get('http://127.0.0.1:19476/__mock/requests')).json();
  expect(openRouterRequests.data.some((item: { path: string; body: string }) => item.path.endsWith('/chat/completions') && item.body.includes('model-a:free'))).toBeTruthy();
  expect(surplusRequests.data.some((item: { path: string; body: string }) => item.path.endsWith('/chat/completions') && item.body.includes('model-a'))).toBeTruthy();
  await page.getByRole('link', { name: 'Models' }).click();
  await expect(page.locator('[data-view-panel="models"] table')).toContainText('Modalities');
  await expect(page.locator('[data-view-panel="models"] table')).toContainText('free-tier');
  await expect(page.locator('#models-table-body .modality-icon[aria-label="Text"]')).toHaveCount(2);
  await expect(page.locator('#models-table-body .modality-icon[aria-label="Image"]')).toHaveCount(1);
  await page.getByRole('link', { name: 'Requests' }).click();
  await page.locator('#refresh-button').click();
  await expect(page.locator('[data-view-panel="requests"] table')).toContainText('Provider');
  await expect(page.locator('[data-view-panel="requests"] table')).toContainText('Attempts');
  await expect(page.locator('[data-view-panel="requests"] table')).toContainText('Surplus Intelligence');
  await expect(page.locator('#status')).toContainText('Ready');
});

test('supports Responses and Anthropic Messages contracts', async ({ page, request }) => {
  await page.goto('/');
  await page.getByRole('link', { name: 'Access & keys' }).click();
  await page.getByRole('button', { name: 'Create API key' }).click();
  await page.locator('#key-label').fill('protocols');
  await page.locator('#key-modal').getByRole('button', { name: 'Create key' }).click();
  await expect(page.locator('#new-key')).toContainText('plai_');
  const secretText = await page.locator('#new-key').textContent();
  const secret = secretText?.match(/plai_[0-9a-f]+/)?.[0];
  expect(secret).toBeTruthy();

  const responses = await request.post('/v1/responses', {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model: 'model-a', input: 'hello' },
  });
  expect(responses.ok()).toBeTruthy();
  expect((await responses.json()).object).toBe('response');

  const messages = await request.post('/v1/messages', {
    headers: { Authorization: `Bearer ${secret}`, 'anthropic-version': '2023-06-01' },
    data: { model: 'model-a', max_tokens: 32, messages: [{ role: 'user', content: 'hello' }] },
  });
  expect(messages.ok()).toBeTruthy();
  expect((await messages.json()).type).toBe('message');
});

test('creates and exposes a callable group alias', async ({ page, request }) => {
  await page.goto('/#groups');
  await expect(page.getByRole('main').getByRole('heading', { name: 'Groups' })).toBeVisible();
  await expect(page.locator('#group-preview')).toHaveCount(0);
  await expect(page.getByText('CALLING A GROUP', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Create group' }).click();
  await expect(page.locator('#group-stage-list .source-search-popover')).toBeHidden();
  await expect(page.locator('#group-stage-list .source-search-toggle')).toBeVisible();
  await expect(page.getByText('Try name', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Description', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Enabled', { exact: true })).toHaveCount(0);
  await expect(page.locator('#group-stage-list .source-candidates')).toBeHidden();
  await expect(page.locator('#group-stage-list .selected-sources .source-empty')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .try-retry-summary')).toBeVisible();
  const blockDuplicate = page.locator('#group-stage-list .stage-card-actions .source-duplicate');
  await expect(blockDuplicate).toHaveAttribute('aria-label', 'Duplicate route block');
  await blockDuplicate.click();
  await expect(page.locator('#group-stage-list .group-stage-card')).toHaveCount(2);
  await page.locator('#group-stage-list .group-stage-card').nth(1).locator('.remove-stage').click();
  await expect(page.locator('#group-stage-list .group-stage-card')).toHaveCount(1);
  await expect(page.getByRole('button', { name: '+ Add route block' })).toBeVisible();
  await expect(page.locator('#group-stage-list .source-kind')).toBeHidden();
  await expect(page.locator('#group-stage-list .stage-billing')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .stage-limit-grid')).toHaveCount(0);
  await page.locator('#group-stage-list .source-search-toggle').click();
  const searchPopoverBounds = await page.locator('#group-stage-list .source-search-popover').evaluate((popover) => {
    const parent = popover.closest('.group-stage-card');
    const popoverRect = popover.getBoundingClientRect();
    const parentRect = parent?.getBoundingClientRect();
    return {
      position: getComputedStyle(popover).position,
      withinParent: Boolean(parentRect && popoverRect.top >= parentRect.top && popoverRect.bottom <= parentRect.bottom),
    };
  });
  expect(searchPopoverBounds.position).toBe('relative');
  expect(searchPopoverBounds.withinParent).toBeTruthy();
  await page.locator('#group-stage-list .source-search').fill('model-a');
  await expect(page.locator('#group-stage-list .source-candidate[data-model-id="model-a"] .candidate-add')).toContainText('Add all routes');
  await expect(page.locator('#group-stage-list .source-candidate[data-model-id="model-a"] .source-route-option')).toHaveCount(2);
  const candidateDiscounts = await page.locator('#group-stage-list .source-route-option .route-discount').allTextContents();
  expect(candidateDiscounts.filter((text) => text.endsWith('%')).every((text) => text.startsWith('-') || text === '0%')).toBeTruthy();
  await page.locator('#group-stage-list .source-candidate[data-model-id="model-a"] .source-route-option[data-add-provider="surplus"]').click();
  await expect(page.locator('#group-stage-list .selected-source')).toHaveCount(1);
  await page.locator('#group-stage-list .source-remove').click();
  await page.locator('#group-stage-list .source-search-toggle').click();
  await page.locator('#group-stage-list .source-search').fill('model-a');
  await page.locator('#group-stage-list .source-candidate[data-model-id="model-a"] .source-candidate-title-row strong').click();
  await expect(page.locator('#group-stage-list .source-search-popover')).toBeHidden();
  await expect(page.locator('#group-stage-list .source-search-toggle')).toBeVisible();
  await page.locator('#group-stage-list .source-search-toggle').click();
  await page.locator('#group-stage-list .source-search').fill('model-a');
  await expect(page.locator('#group-stage-list .source-search-popover')).toBeVisible();
  await page.locator('#group-name').click();
  await expect(page.locator('#group-stage-list .source-search-popover')).toBeHidden();
  await expect(page.locator('#group-name')).not.toHaveValue('');
  await expect(page.locator('#group-slug')).toHaveValue(/^[a-z0-9-]+$/);
  await page.locator('#group-name').fill('Coding pool');
  await expect(page.locator('#group-slug')).toHaveValue('coding-pool');
  await expect(page.locator('#group-stage-list .selected-source').first()).toContainText('Model A');
  await expect(page.locator('#group-stage-list .selected-source').first()).not.toContainText('model-a');
  await expect(page.locator('#group-stage-list .selected-route-heading')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .selected-source')).toHaveCount(3);
  await expect(page.locator('#group-stage-list .selected-source').first()).toHaveAttribute('draggable', 'false');
  await expect(page.locator('#group-stage-list .selected-source').first().locator('.drag-handle')).toHaveAttribute('draggable', 'true');
  await expect(page.locator('#group-stage-list .selected-source .drag-handle')).toHaveCount(3);
  await expect(page.locator('#group-stage-list .selected-source .drag-handle').first()).toHaveAttribute('draggable', 'true');
  await expect(page.locator('#group-stage-list .source-include-new').first()).toBeChecked();
  const futureProviderRow = page.locator('#group-stage-list .selected-source').filter({ hasText: 'New providers' }).first();
  await expect(futureProviderRow.locator('.source-remove, .source-duplicate, .retry-summary')).toHaveCount(0);
  await expect(futureProviderRow.locator('.selected-source-header-actions .source-include-new')).toHaveCount(1);
  await expect(futureProviderRow.locator('.new-provider-explanation')).toContainText('When a new provider with Model A is added, it is automatically added to this group.');
  await expect(futureProviderRow.locator('.tooltip-help')).toHaveCount(0);
  await expect(futureProviderRow).toHaveClass(/new-provider-enabled/);
  const providerSwitch = page.locator('#group-stage-list .include-checkbox-mark').first();
  await expect(providerSwitch).toBeVisible();
  const switchMetrics = await providerSwitch.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const style = getComputedStyle(element);
    return { width: rect.width, height: rect.height, radius: style.borderRadius };
  });
  expect(switchMetrics.width).toBeGreaterThan(30);
  expect(switchMetrics.height).toBeGreaterThan(15);
  expect(switchMetrics.radius).toBe('999px');
  await expect(page.locator('#group-stage-list .provider-scope-note')).toHaveCount(0);
  await page.locator('#group-stage-list .source-include-new').first().uncheck();
  await expect(page.locator('#group-stage-list .selected-source').filter({ hasText: 'New providers' }).first()).toHaveClass(/new-provider-disabled/);
  await expect(page.locator('#group-stage-list .selected-source .source-duplicate')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .stage-card-actions .source-duplicate')).toHaveAttribute('title', 'Duplicate route block');
  await expect(page.locator('#group-stage-list .selected-source-header-actions .source-remove')).toHaveCount(2);
  await expect(page.locator('#group-stage-list .source-remove').first()).toHaveAttribute('aria-label', 'Remove provider route');
  await expect(page.locator('#group-stage-list .source-remove').first()).toHaveAttribute('title', 'Remove provider route');
  const selectedPrices = await page.locator('#group-stage-list .compact-route-summary').allTextContents();
  expect(selectedPrices.every((text) => !text.includes('official'))).toBeTruthy();
  const selectedProviders = await page.locator('#group-stage-list .selected-route-provider').allTextContents();
  expect(selectedProviders).toContain('Surplus Intelligence');
  const auctionCapText = await page.locator('#group-stage-list .auction-cap-line').allTextContents();
  expect(auctionCapText).toHaveLength(1);
  expect(auctionCapText[0]).toContain('Max pricing');
  expect(auctionCapText[0]).not.toContain('OpenRouter');
  expect(auctionCapText[0]).not.toContain('Surplus Intelligence');
  expect(auctionCapText[0]).toMatch(/\$[0-9.]+ in \/ \$[0-9.]+ out/);
  const auctionSlider = page.locator('#group-stage-list .stage-max-price-percent').first();
  await expect(page.locator('#group-stage-list .max-price-value').first()).toHaveText('100% of model official');
  const capStatus = page.locator('#group-stage-list .auction-cap-status').first();
  await expect(capStatus).toHaveText(/(\d+ providers?|No providers) included under this max price setting/);
  const initiallyClassifiedRoutes = await page.locator('#group-stage-list .selected-source.cap-included, #group-stage-list .selected-source.cap-excluded, #group-stage-list .selected-source.cap-not-applicable').count();
  expect(initiallyClassifiedRoutes).toBe(3);
  await auctionSlider.scrollIntoViewIfNeeded();
  const sliderBox = await auctionSlider.boundingBox();
  expect(sliderBox).not.toBeNull();
  if (!sliderBox) throw new Error('auction slider is not measurable');
  await page.mouse.move(sliderBox.x + sliderBox.width - 3, sliderBox.y + sliderBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(sliderBox.x + sliderBox.width * 0.55, sliderBox.y + sliderBox.height / 2, { steps: 12 });
  await page.mouse.up();
  await expect(auctionSlider).not.toHaveValue('100');
  await expect(page.locator('#group-stage-list .max-price-value').first()).toContainText('of model official');
  const selectedPricePercent = Number(await auctionSlider.inputValue());
  await expect(capStatus).toHaveText(/(\d+ providers?|No providers) included under this max price setting/);
  const openRouterRoute = page.locator('#group-stage-list .selected-source').filter({ hasText: 'OpenRouter' }).first();
  await expect(openRouterRoute).toHaveClass(/cap-(included|excluded)/);
  const dynamicallyClassifiedRoutes = await page.locator('#group-stage-list .selected-source.cap-included, #group-stage-list .selected-source.cap-excluded, #group-stage-list .selected-source.cap-not-applicable').count();
  expect(dynamicallyClassifiedRoutes).toBe(3);
  await page.locator('#group-stage-list .source-include-new').first().uncheck();
  await expect(page.locator('#group-stage-list .provider-scope-note')).toHaveCount(0);
  await page.locator('#group-stage-list .source-include-new').first().check();
  const retrySummary = page.locator('#group-stage-list .retry-summary').first();
  await expect(retrySummary).toContainText('1');
  await retrySummary.click();
  await expect(page.locator('#retry-modal')).toBeVisible();
  await expect(page.locator('#retry-count')).toHaveValue('1');
  await page.locator('#retry-count').fill('2');
  await page.getByRole('button', { name: 'Save retries' }).click();
  await expect(page.locator('#group-stage-list .retry-summary').first()).toContainText('2');
  await page.locator('#group-stage-list .try-retry-summary').click();
  await expect(page.locator('#retry-modal-title')).toHaveText('Retry this route block');
  await page.locator('#retry-count').fill('2');
  await page.getByRole('button', { name: 'Save retries' }).click();
  await expect(page.locator('#group-stage-list .try-retry-summary')).toContainText('2');
  await page.getByRole('button', { name: 'Save group' }).click();
  await expect(page.locator('#groups-list')).toContainText('coding-pool');
  await expect(page.locator('#group-editor')).toBeHidden();
  const savedGroup = await (await request.get('/api/groups')).json();
  const savedSources = savedGroup.data.find((item: { slug: string }) => item.slug === 'coding-pool').stages[0].sources;
  expect(savedSources).toHaveLength(3);
  expect(savedSources).toEqual(expect.arrayContaining([
    expect.objectContaining({ model_id: 'model-a', provider_name: 'surplus', maximum_official_price_percent: selectedPricePercent }),
    expect.objectContaining({ model_id: 'model-a', provider_name: 'openrouter', maximum_official_price_percent: selectedPricePercent }),
    expect.objectContaining({ model_id: 'model-a', include_new_providers: true, maximum_official_price_percent: selectedPricePercent }),
  ]));
  const groupToggle = page.locator('#groups-list [data-toggle-group]').first();
  await expect(groupToggle).toHaveText('');
  await expect(groupToggle).toHaveAttribute('role', 'switch');
  await expect(groupToggle).toHaveAttribute('aria-checked', 'true');
  const groupCopy = page.locator('#groups-list .group-slug-row .group-copy');
  await expect(groupCopy).toHaveCount(1);
  await expect(groupCopy).toHaveAttribute('aria-label', 'Copy group slug');
  await expect(groupCopy).toHaveText('⧉');
  const connectGroup = page.locator('#groups-list [data-connect-group]').first();
  await expect(connectGroup).toContainText('Connection instructions');
  await expect(connectGroup).toHaveClass(/group-connect-link/);
  await expect(connectGroup).toHaveAttribute('title', 'Connection instructions');
  await expect(connectGroup.locator('.group-connect-help')).toBeVisible();
  await expect(connectGroup.locator('.group-connect-arrow')).toHaveText('→');
  await expect(page.locator('#groups-list .group-actions [data-connect-group]')).toHaveCount(0);
  await expect(page.locator('#groups-list .group-title-row .group-toggle')).toHaveCount(1);
  await connectGroup.click();
  const instructions = page.locator('#group-instructions-modal');
  await expect(instructions).toBeVisible();
  await expect(instructions).toContainText('Coding pool');
  await expect(page.locator('#group-instructions-base-url')).toHaveText(/\/v1$/);
  await expect(page.locator('#group-instructions-model')).toHaveText('coding-pool');
  await expect(page.locator('#group-instructions-curl')).toContainText('/chat/completions');
  await expect(page.locator('#group-instructions-curl')).toContainText('coding-pool');
  await page.getByRole('button', { name: 'Close connection instructions' }).click();
  await expect(instructions).toBeHidden();
  await page.setViewportSize({ width: 900, height: 800 });
  const actionRows = await page.locator('#groups-list .group-actions').first().evaluate((actions) => {
    const buttons = [...actions.querySelectorAll('button')].map((button) => ({ text: button.textContent?.trim(), top: Math.round(button.getBoundingClientRect().top), width: Math.round(button.getBoundingClientRect().width) }));
    const tops = buttons.map((button) => button.top);
    return { rowSpan: Math.max(...tops) - Math.min(...tops), overflow: actions.scrollWidth > actions.clientWidth, buttons };
  });
  expect(actionRows.rowSpan).toBeLessThan(8);
  expect(actionRows.overflow).toBeFalsy();
  await page.setViewportSize({ width: 1280, height: 800 });
  const titleToggleLayout = await page.locator('#groups-list .group-title-row').first().evaluate((row) => {
    const title = row.querySelector('strong')?.getBoundingClientRect();
    const toggle = row.querySelector('.group-toggle')?.getBoundingClientRect();
    return { gap: title && toggle ? toggle.left - title.right : Number.POSITIVE_INFINITY, overflow: row.scrollWidth > row.clientWidth };
  });
  expect(titleToggleLayout.gap).toBeGreaterThanOrEqual(0);
  expect(titleToggleLayout.gap).toBeLessThan(32);
  expect(titleToggleLayout.overflow).toBeFalsy();
  const connectionLayout = await page.locator('#groups-list .group-row').first().evaluate((row) => {
    const card = row.getBoundingClientRect();
    const connection = row.querySelector('[data-connect-group]')?.getBoundingClientRect();
    return { rightGap: card && connection ? card.right - connection.right : Number.POSITIVE_INFINITY };
  });
  expect(connectionLayout.rightGap).toBeGreaterThanOrEqual(10);
  expect(connectionLayout.rightGap).toBeLessThan(18);
  await groupToggle.click();
  await expect(page.locator('#groups-list [data-toggle-group]').first()).toHaveText('');
  await expect(page.locator('#groups-list [data-toggle-group]').first()).toHaveAttribute('aria-checked', 'false');
  const disabledGroup = await (await request.get('/api/groups')).json();
  expect(disabledGroup.data.find((item: { slug: string }) => item.slug === 'coding-pool').enabled).toBeFalsy();
  await page.locator('#groups-list [data-toggle-group]').first().click();
  await expect(page.locator('#groups-list [data-toggle-group]').first()).toHaveText('');
  await expect(page.locator('#groups-list [data-toggle-group]').first()).toHaveAttribute('aria-checked', 'true');
  const groupSurface = await page.locator('.group-row').first().evaluate((element) => {
    const style = getComputedStyle(element);
    return { color: style.color, backgroundImage: style.backgroundImage };
  });
  expect(groupSurface.color).toBe('rgb(244, 247, 251)');
  expect(groupSurface.backgroundImage).toContain('linear-gradient');
  const helperGroup = await request.post('/api/groups', {
    data: {
      name: 'Model A fallback',
      slug: 'model-a-fallback',
      description: '',
      enabled: true,
      stages: [{ position: 0, name: '', sources: [{ kind: 'model', model_id: 'model-a', provider_name: 'openrouter', retries: 1 }], billing_classes: ['free', 'subscription', 'metered'], selection: 'lowest_expected_cost', try_retries: 1 }],
    },
  });
  expect(helperGroup.ok()).toBeTruthy();
  await page.reload();
  await page.getByRole('button', { name: 'Create group' }).click();
  await page.locator('#group-stage-list .source-search-toggle').click();
  await page.locator('#group-stage-list .source-search').fill('model-a');
  await expect(page.locator('#group-stage-list .source-candidate').first()).toHaveClass(/source-group-candidate/);
  await page.locator('#close-group-editor').click();
  await page.getByRole('button', { name: 'Create group' }).click();
  const editorHeadingBox = await page.locator('#group-editor .panel-heading').boundingBox();
  const groupNameBox = await page.locator('#group-name').boundingBox();
  expect(editorHeadingBox).not.toBeNull();
  expect(groupNameBox).not.toBeNull();
  if (!editorHeadingBox || !groupNameBox) throw new Error('group editor alignment is not measurable');
  expect(Math.abs(editorHeadingBox.x - groupNameBox.x)).toBeLessThan(2);
  const identityOrder = await page.locator('#group-form').evaluate((form) => {
    const children = [...form.children].map((child) => child.id || child.className);
    return { stage: children.indexOf('group-stage-list'), identity: children.indexOf('group-identity-fields') };
  });
  expect(identityOrder.identity).toBeGreaterThan(identityOrder.stage);
  const groupInputSurface = await page.locator('#group-name').evaluate((element) => {
    const style = getComputedStyle(element);
    return { color: style.color, backgroundColor: style.backgroundColor };
  });
  expect(groupInputSurface).toEqual({ color: 'rgb(244, 247, 251)', backgroundColor: 'rgb(10, 17, 34)' });
  await page.locator('#close-group-editor').click();
  const models = await (await request.get('/v1/models')).json();
  expect(models.data.some((item: { id: string; paylessforai_type?: string }) => item.id === 'coding-pool' && item.paylessforai_type === 'group')).toBeTruthy();
});

test('keeps provider verification errors visible and verifies manual models before saving', async ({ page, request }) => {
  await request.post('http://127.0.0.1:19475/__mock/scenario', {
    data: { models: [], response_text: 'manual response' },
  });
  await page.goto('/#access');
  await page.getByRole('button', { name: 'Add provider' }).click();
  await page.locator('#provider-type').selectOption('custom');
  await page.locator('#provider-name').fill('manual-mock');
  await page.locator('#provider-base-url').fill('http://127.0.0.1:19475/manual/v1');
  await page.locator('#provider-label').fill('Manual test');
  await page.locator('#provider-key').fill('manual-key');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-feedback')).toContainText('no models');
  await expect(page.locator('#manual-model-fields')).toBeVisible();
  await page.locator('#manual-models').fill('manual-model | 1.00 | 2.00');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-list')).toContainText('manual-mock');
  await page.goto('/#models');
  await expect(page.locator('#models-table-body')).toContainText('manual-model');
});

test('configures a subscription, records quota blocking, and shows dynamic pricing', async ({ page, request }) => {
  await request.post('http://127.0.0.1:19474/__mock/scenario', { data: { models: [{ id: 'subscription-model', name: 'Subscription Model', prompt_price: '0.000001', completion_price: '0.000002', context_length: 128000, max_completion_tokens: 4096 }], response_text: 'subscription response', input_tokens: 1000, output_tokens: 500 } });
  await page.goto('/#access');
  await page.getByRole('button', { name: 'Add provider' }).click();
  await expect(page.locator('#provider-access-mode')).toHaveValue('api');
  await page.locator('#provider-access-mode').selectOption('subscription');
  await expect(page.locator('#subscription-fields')).toBeVisible();
  await page.locator('#subscription-fee').fill('20');
  await page.screenshot({ path: 'artifacts/subscription-provider-modal.png', fullPage: true });
  await page.locator('#provider-type').selectOption('custom');
  await page.locator('#provider-name').fill('subscription-mock');
  await page.locator('#provider-base-url').fill('http://127.0.0.1:19474/custom/v1');
  await page.locator('#provider-label').fill('Pro plan');
  await page.locator('#provider-key').fill('subscription-key');
  await page.getByRole('button', { name: 'Verify & save' }).click();
  await expect(page.locator('#provider-feedback')).toContainText('Found 1 model');
  await expect(page.locator('#provider-list')).toContainText('subscription-mock');
  const configured = await (await request.get('/api/providers/credentials')).json();
  expect(configured.data.find((item: { provider: string }) => item.provider === 'subscription-mock')).toMatchObject({ access_mode: 'subscription', subscription_fee_pico_usd: 20000000000000 });

  await page.getByRole('button', { name: 'Create API key' }).click();
  await page.locator('#key-label').fill('subscription-e2e');
  await page.locator('#key-modal').getByRole('button', { name: 'Create key' }).click();
  await expect(page.locator('#new-key')).toContainText('plai_');
  const secret = (await page.locator('#new-key').textContent())?.match(/plai_[0-9a-f]+/)?.[0];
  expect(secret).toBeTruthy();
  await page.locator('#key-modal').getByRole('button', { name: 'Close' }).click();
  const success = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model: 'subscription-model', messages: [{ role: 'user', content: 'hello' }] } });
  expect(success.ok()).toBeTruthy();

  await request.post('http://127.0.0.1:19474/__mock/scenario', { data: { models: [{ id: 'subscription-model', name: 'Subscription Model', prompt_price: '0.000001', completion_price: '0.000002' }], status: 429, failure_message: 'monthly usage quota exceeded' } });
  const limited = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model: 'subscription-model', messages: [{ role: 'user', content: 'again' }] } });
  expect(limited.status()).toBe(429);
  expect((await limited.json()).error.code).toBe('provider_quota_exhausted');
  const credentials = await (await request.get('/api/providers/credentials')).json();
  expect(credentials.data.find((item: { provider: string }) => item.provider === 'subscription-mock')).toMatchObject({ access_mode: 'subscription', subscription_status: 'limited' });
  const summary = await (await request.get('/api/stats/summary')).json();
  expect(summary.excluded_limit_requests).toBeGreaterThanOrEqual(1);

  // Subscription provider-model routes are explicit single-route blocks: no
  // model-wide provider switch or price-cap controls are shown.
  await page.goto('/#groups');
  await page.getByRole('button', { name: 'Create group' }).click();
  await page.locator('#group-stage-list .source-search-toggle').click();
  await page.locator('#group-stage-list .source-search').fill('subscription-model');
  const subscriptionSearchRoute = page.locator('#group-stage-list .source-candidate[data-model-id="subscription-model"] .source-route-option[data-add-provider="subscription-mock"] .route-price-line');
  await expect(subscriptionSearchRoute).toContainText('SUBSCRIPTION');
  await expect(subscriptionSearchRoute).not.toContainText('$');
  await expect(subscriptionSearchRoute.locator('.route-discount')).toHaveCount(0);
  await page.locator('#group-stage-list .source-candidate[data-model-id="subscription-model"] .source-route-option[data-add-provider="subscription-mock"]').click();
  await expect(page.locator('#group-stage-list .selected-source')).toHaveCount(1);
  await expect(page.locator('#group-stage-list .selected-source .subscription-warning')).toContainText('may be rate limited');
  await expect(page.locator('#group-stage-list .selected-source .source-include-new')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .selected-source .all-provider-pricing')).toHaveCount(0);
  await expect(page.locator('#group-stage-list .selected-source .selected-provider-routes')).toHaveCount(0);

  await page.getByRole('link', { name: 'Statistics' }).click();
  await page.locator('#refresh-button').click();
  await expect(page.locator('[data-view-panel="stats"]')).toContainText('Subscription economics');
  await expect(page.locator('[data-view-panel="stats"]')).toContainText('Pro plan');
  await page.screenshot({ path: 'artifacts/subscription-statistics.png', fullPage: true });
});
