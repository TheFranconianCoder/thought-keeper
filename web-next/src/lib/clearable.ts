import { t } from './i18n/index.svelte';

/** clearable adds the "clear" (×) button every text field should have.
 *
 *  It wraps the field in a small positioned host and appends the button after it, so the button can be
 *  positioned over the field's right edge and the field keeps its layout slot. Visibility is CSS-only
 *  (`:placeholder-shown`), which means a field whose value is cleared PROGRAMMATICALLY — a submitted
 *  question, a reset filter — loses the button too, without the action having to poll the value.
 *
 *  Checkboxes, radios, file pickers and the like are left alone: they hold a choice, not text.
 */
type Field = HTMLInputElement | HTMLTextAreaElement;

const NOT_TEXT = new Set([
  'checkbox',
  'radio',
  'file',
  'hidden',
  'range',
  'color',
  'submit',
  'button',
  'reset',
  'image',
]);

export function clearable(node: Field) {
  if (node instanceof HTMLInputElement && NOT_TEXT.has(node.type)) {
    return {};
  }

  const host = document.createElement('span');
  host.className = 'clearable';
  host.style.display = 'block';
  host.style.position = 'relative';
  host.style.minWidth = '0';
  // A no-op in a block parent; what makes the field fill a flex row (the palette, the ask composer).
  host.style.flex = '1 1 auto';

  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'field-clear';
  button.setAttribute('aria-label', t('common.clearField'));
  button.title = t('common.clearField');
  // The app's own close glyph, so the × matches the icon set instead of being a text character.
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 20 20');
  svg.setAttribute('width', '12');
  svg.setAttribute('height', '12');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.6');
  svg.setAttribute('stroke-linecap', 'round');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  path.setAttribute('d', 'M5 5l10 10 M15 5 5 15');
  svg.appendChild(path);
  button.appendChild(svg);

  node.parentNode?.insertBefore(host, node);
  host.append(node, button);

  // The field must fill its host (a flex:1 field loses that once it is wrapped), and the button needs
  // room; both are inline so the action works with any of the app's input classes.
  node.classList.add('field-clearable');
  const wasWidth = node.style.width;
  const wasPad = node.style.paddingRight;
  node.style.width = '100%';
  node.style.paddingRight = '30px';
  if (node instanceof HTMLTextAreaElement) button.classList.add('field-clear--area');

  // `:placeholder-shown` needs a placeholder to be shown. Every field in the app has one; this keeps a
  // future field without one from losing its clear button.
  if (!node.getAttribute('placeholder')) node.setAttribute('placeholder', ' ');

  const clear = (event: MouseEvent): void => {
    event.preventDefault();
    event.stopPropagation();
    if (node.disabled || node.readOnly) return;
    node.value = '';
    node.dispatchEvent(new Event('input', { bubbles: true }));
    node.focus();
  };
  // Keep the field focused: pressing the button must not blur it first.
  const keepFocus = (event: MouseEvent): void => event.preventDefault();

  button.addEventListener('mousedown', keepFocus);
  button.addEventListener('click', clear);

  return {
    destroy(): void {
      button.removeEventListener('mousedown', keepFocus);
      button.removeEventListener('click', clear);
      node.classList.remove('field-clearable');
      node.style.width = wasWidth;
      node.style.paddingRight = wasPad;
      // Put the field back where the host stood, THEN remove the host. This used to call
      // node.parentNode.insertBefore(node, host) — the parent is the host itself, so the
      // reference child was not a child of the node the call was made on. The DOM threw
      // NotFoundError during teardown, which aborted the view change and left the main area
      // blank until a full reload.
      const parent = host.parentNode;
      if (parent && node.parentNode === host) parent.insertBefore(node, host);
      host.remove();
    },
  };
}
