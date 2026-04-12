/**
 * Types for the filter return values and the cursor types
 */
type FilterResult = boolean | 'skip' | 'quit' | string | undefined | null;
type CursorType = 'empty' | 'text' | 'element';
type FilterFunction = (cursor: DOMCursor) => FilterResult;

class DOMCursor {
  public node: Node | null;
  public pos: number;
  public filter: FilterFunction;
  public type: CursorType = 'empty';

  constructor(node: Node | null, pos?: number, filter?: FilterFunction) {
    this.node = node;
    this.pos = pos ?? 0;
    this.filter = filter || (() => true);
    this.computeType();
  }

  computeType(): this {
    if (!this.node) {
      this.type = 'empty';
    } else if (this.node.nodeType === Node.TEXT_NODE) {
      this.type = 'text';
    } else {
      this.type = 'element';
    }
    return this;
  }

  newPos(node: Node | null, pos: number): DOMCursor {
    return new DOMCursor(node, pos, this.filter);
  }

  isEmpty(): boolean {
    return this.type === 'empty';
  }

  setFilter(f: FilterFunction): DOMCursor {
    return new DOMCursor(this.node, this.pos, f);
  }

  addFilter(filt: FilterFunction): DOMCursor {
    const oldFilt = this.filter;
    return this.setFilter((n: DOMCursor) => {
      const r1 = oldFilt(n);
      if (r1 === 'quit' || r1 === 'skip') return r1;
      
      const r2 = filt(n);
      if (r2 === 'quit' || r2 === 'skip') return r2;
      
      return !!(r1 && r2);
    });
  }

  next(up?: boolean): DOMCursor {
    const saved = this.save();
    let n = this.nodeAfter(up);
    while (!n.isEmpty()) {
      const res = n.filter(n);
      switch (res) {
        case 'skip':
          n = n.nodeAfter(true);
          continue;
        case 'quit':
          return this.restore(saved).emptyNext();
        default:
          if (res) return n;
      }
      n = n.nodeAfter();
    }
    return this.restore(saved).emptyNext();
  }

  prev(up?: boolean): DOMCursor {
    const saved = this.save();
    let n = this.nodeBefore(up);
    while (!n.isEmpty()) {
      const res = n.filter(n);
      switch (res) {
        case 'skip':
          n = n.nodeBefore(true);
          continue;
        case 'quit':
          return this.restore(saved).emptyPrev();
        default:
          if (res) return n;
      }
      n = n.nodeBefore();
    }
    return this.restore(saved).emptyPrev();
  }

  moveCaret(r?: Range): this {
    if (!r) r = document.createRange();
    if (this.node) {
      r.setStart(this.node, this.pos);
      r.collapse(true);
      DOMCursor.selectRange(r);
    }
    return this;
  }

  firstText(backwards?: boolean): DOMCursor {
    let n: DOMCursor = this;
    while (!n.isEmpty() && n.type !== 'text') {
      n = backwards ? n.prev() : n.next();
    }
    return n;
  }

  countChars(node: Node, pos: number): number {
    let n: DOMCursor = this;
    let tot = 0;
    while (!n.isEmpty() && n.node !== node) {
      if (n.type === 'text') tot += (n.node as Text).length;
      n = n.next();
    }
    if (n.isEmpty() || n.node !== node) return -1;
    return n.type === 'text' ? tot + pos : tot;
  }

  forwardChars(count: number, contain?: boolean): DOMCursor {
    let n: DOMCursor = this;
    while (!n.isEmpty() && 0 <= count) {
      if (n.type === 'text') {
        const len = (n.node as Text).length;
        if (count < len) {
          if (count === 0 && contain) {
            n = n.prev();
            while (n.type !== 'text') n = n.prev();
            return n.newPos(n.node, (n.node as Text).length);
          }
          return n.newPos(n.node, count);
        }
        count -= len;
      }
      n = n.next();
    }
    return n.emptyNext();
  }

  hasAttribute(a: string): boolean {
    return (this.node?.nodeType === Node.ELEMENT_NODE) && (this.node as Element).hasAttribute(a);
  }

  getAttribute(a: string): string | null {
    return (this.node?.nodeType === Node.ELEMENT_NODE) ? (this.node as Element).getAttribute(a) : null;
  }

  filterTextNodes(): DOMCursor {
    return this.addFilter((n) => n.type === 'text');
  }

  filterVisibleTextNodes(): DOMCursor {
    return this.filterTextNodes().addFilter((n) => !DOMCursor.isCollapsed(n.node));
  }

  filterParent(parent: Node | null): DOMCursor {
    if (!parent) return this.setFilter(() => 'quit');
    return this.addFilter((n) => (n.node && parent.contains(n.node)) || 'quit');
  }

  filterRange(range: Range): DOMCursor;
  filterRange(startContainer: Node, startOffset: number, endContainer: Node, endOffset: number): DOMCursor;
  filterRange(startContainerOrRange: Node | Range, startOffset?: number, endContainer?: Node, endOffset?: number): DOMCursor {
    let sc: Node, so: number, ec: Node, eo: number;

    if (startContainerOrRange instanceof Range) {
      sc = startContainerOrRange.startContainer;
      so = startContainerOrRange.startOffset;
      ec = startContainerOrRange.endContainer;
      eo = startContainerOrRange.endOffset;
    } else if (startOffset !== undefined && endContainer && endOffset !== undefined) {
      sc = startContainerOrRange;
      so = startOffset;
      ec = endContainer;
      eo = endOffset;
    } else {
      return this;
    }

    return this.addFilter((n) => {
      if (!n.node) return 'quit';
      const startPos = sc.compareDocumentPosition(n.node);
      if (startPos === 0) {
        return (so <= n.pos && n.pos <= eo) || 'quit';
      } else if (startPos & Node.DOCUMENT_POSITION_FOLLOWING) {
        const endPos = ec.compareDocumentPosition(n.node);
        if (endPos === 0) return (n.pos <= eo) || 'quit';
        return (endPos & Node.DOCUMENT_POSITION_PRECEDING) ? true : 'quit';
      }
      return 'quit';
    });
  }

  getText(): string {
    let n = this.mutable().firstText();
    if (n.isEmpty()) return '';
    
    let t = (n.node as Text).data.substring(n.pos);
    while (!n.next().isEmpty()) {
      if (n.type === 'text') t += (n.node as Text).data.substring(n.pos);
    }

    if (t.length) {
      while (n.type !== 'text') n.prev();
      n.pos = (n.node as Text).length;
      while (n.pos > 0 && DOMCursor.reject(n.filter(n))) {
        n.pos--;
      }
      return t.substring(0, t.length - (n.node as Text).length + n.pos);
    }
    return '';
  }

  isNL(): boolean {
    return this.type === 'text' && (this.node as Text).data[this.pos] === '\n';
  }

  endsInNL(): boolean {
    return this.type === 'text' && (this.node as Text).data[(this.node as Text).length - 1] === '\n';
  }

  moveToStart(): DOMCursor {
    return this.newPos(this.node, 0);
  }

  moveToNextStart(): DOMCursor {
    return this.next().moveToStart();
  }

  moveToEnd(): DOMCursor {
    if (!this.node) return this;
    const end = (this.node as Text).length - (this.endsInNL() ? 1 : 0);
    return this.newPos(this.node, end);
  }

  moveToPrevEnd(): DOMCursor {
    return this.prev().moveToEnd();
  }

  forwardLine(goalFunc: (left: number) => number = () => -1): DOMCursor {
    let r = this.charRect();
    let bottom = r?.bottom;
    let line = 0;
    let n: DOMCursor = this;
    
    while (n = n.forwardChar()) {
      if (n.isEmpty()) return n.backwardChar();
      const nr = n.charRect();
      if (nr && nr.bottom !== bottom) {
        bottom = nr.bottom;
        line++;
      }
      if (line === 1 && nr && goalFunc(nr.left) > -1) return n;
      if (line === 2) return n.backwardChar();
    }
    return n;
  }

  backwardLine(goalFunc: (left: number) => number = () => -1): DOMCursor {
    let r = this.charRect();
    let prevTop = r?.top;
    let top = r?.top;
    let line = 0;
    let n: DOMCursor = this;

    while (n = n.backwardChar()) {
      if (n.isEmpty()) return n.forwardChar();
      const nr = n.charRect();
      if (nr && nr.top !== top) {
        top = nr.top;
        line++;
      }
      if (line === 1 && nr) {
        switch (goalFunc(nr.left)) {
          case 0: return n;
          case -1: return (prevTop === top) ? n.forwardChar() : n;
        }
      }
      if (line === 2) return n.forwardChar();
      prevTop = top;
    }
    return n;
  }

  forwardChar(): DOMCursor {
    let r = DOMCursor.stubbornCharRectNext(this.node, this.pos);
    const left = r?.left;
    const bottom = r?.bottom;
    let n: DOMCursor = this;

    while (n = (n.pos + 1 < (n.node as Text).length ? n.newPos(n.node, n.pos + 1) : n.next())) {
      const nr = DOMCursor.stubbornCharRectNext(n.node, n.pos);
      if (n.isEmpty() || (nr && (left !== nr.left || bottom !== nr.bottom))) return n;
    }
    return n;
  }

  backwardChar(): DOMCursor {
    let r = DOMCursor.stubbornCharRectPrev(this.node, this.pos);
    let n: DOMCursor = this;
    while (r && (n = (n.pos > 0 ? n.newPos(n.node, n.pos - 1) : n.prev()))) {
      if (n.isEmpty() || n.moved(r)) return n;
    }
    return n;
  }

  show(topRect?: DOMRect): this {
    const posRect = this.charRect();
    if (!posRect) return this;
    const top = (topRect?.width && topRect.top === 0) ? topRect.bottom : 0;
    if (posRect.bottom > window.innerHeight) window.scrollBy(0, posRect.bottom - window.innerHeight);
    else if (posRect.top < top) window.scrollBy(0, posRect.top - top);
    return this;
  }

  immutable(): DOMCursor { return this; }
  copy(): DOMCursor { return this; }
  save(): DOMCursor { return this; }
  restore(n: DOMCursor): DOMCursor { return n.immutable(); }

  withMutations<T>(func: (m: MutableDOMCursor) => T): T {
    return func(this.copy().mutable());
  }

  mutable(): MutableDOMCursor {
    return new MutableDOMCursor(this.node, this.pos, this.filter);
  }

  nodeAfter(up?: boolean): DOMCursor {
    let node = this.node;
    while (node) {
      if (node.nodeType === Node.ELEMENT_NODE && !up && node.childNodes.length) {
        return this.newPos(node.childNodes[0], 0);
      } else if (node.nextSibling) {
        return this.newPos(node.nextSibling, 0);
      } else {
        up = true;
        node = node.parentNode;
      }
    }
    return this.emptyNext();
  }

  emptyNext(): DOMCursor {
    const empty = new EmptyDOMCursor();
    empty.filter = this.filter;
    empty.prev = (up?: boolean) => up ? this.prev(up) : this;
    empty.nodeBefore = (up?: boolean) => up ? this.nodeBefore(up) : this;
    return empty;
  }

  nodeBefore(up?: boolean): DOMCursor {
    let node = this.node;
    while (node) {
      if (node.nodeType === Node.ELEMENT_NODE && !up && node.childNodes.length) {
        const newNode = node.childNodes[node.childNodes.length - 1];
        return this.newPos(newNode, (newNode as any).length || 0);
      } else if (node.previousSibling) {
        const newNode = node.previousSibling;
        return this.newPos(newNode, (newNode as any).length || 0);
      } else {
        up = true;
        node = node.parentNode;
        continue;
      }
    }
    return this.emptyPrev();
  }

  emptyPrev(): DOMCursor {
    const empty = new EmptyDOMCursor();
    empty.filter = this.filter;
    empty.next = (up?: boolean) => up ? this.next(up) : this;
    empty.nodeAfter = (up?: boolean) => up ? this.nodeAfter(up) : this;
    return empty;
  }

  moved(rec: DOMRect): boolean {
    if (!this.node) return false;
    const r2 = DOMCursor.stubbornCharRectPrev(this.node, this.pos);
    return !!((this.node as Text).length > this.pos && r2 && (rec.top !== r2.top || rec.left !== r2.left));
  }

  charRect(r?: Range, prev?: boolean): DOMRect | undefined {
    if (!this.node) return undefined;
    if (prev) {
      return DOMCursor.stubbornCharRectPrev(this.node, this.pos, r) || 
             DOMCursor.stubbornCharRectNext(this.node, this.pos, r);
    }
    return DOMCursor.charRect(this.node, this.pos, r);
  }

  // --- Static Utilities ---

  static isCollapsed(node: Node | null): boolean {
    if (!node) return false;
    const type = node.nodeType;
    return type === 7 || type === 8 ||
      (type === Node.TEXT_NODE && ((node as Text).data === '' || this.isCollapsed(node.parentNode))) ||
      /^(script|style)$/i.test(node.nodeName) ||
      (type === Node.ELEMENT_NODE && (node as HTMLElement).offsetHeight === 0);
  }

  static selectRange(r: Range): void {
    const sel = getSelection();
    if (sel) {
      sel.removeAllRanges();
      sel.addRange(r);
    }
  }

  static reject(filterResult: FilterResult): boolean {
    return !filterResult || filterResult === 'quit' || filterResult === 'skip';
  }

  static stubbornCharRectNext(node: Node | null, pos: number, r?: Range): DOMRect | undefined {
    if (!node) return undefined;
    r = r || document.createRange();
    const len = (node as any).length || 0;
    for (let i = pos; i < len; i++) {
      const rec = this.charRect(node, i, r);
      if (rec) return rec;
    }
    return undefined;
  }

  static stubbornCharRectPrev(node: Node | null, pos: number, r?: Range): DOMRect | undefined {
    if (!node) return undefined;
    r = r || document.createRange();
    for (let i = pos; i >= 0; i--) {
      const rec = this.charRect(node, i, r);
      if (rec) return rec;
    }
    return undefined;
  }

  static charRect(node: Node, pos: number, r?: Range): DOMRect | undefined {
    r = r || document.createRange();
    try {
      r.setStart(node, pos);
      r.collapse(true);
      const rects = r.getClientRects();
      return rects[rects.length - 1];
    } catch { return undefined; }
  }
}

class EmptyDOMCursor extends DOMCursor {
  constructor() { super(null); }
  moveCaret(): this { return this; }
  show(): this { return this; }
  nodeAfter(): DOMCursor { return this; }
  nodeBefore(): DOMCursor { return this; }
  next(): DOMCursor { return this; }
  prev(): DOMCursor { return this; }
}

class MutableDOMCursor extends DOMCursor {
  constructor(node: Node | null, pos?: number, filter?: FilterFunction) {
    super(node, pos, filter);
  }

  setFilter(f: FilterFunction): this {
    this.filter = f;
    return this;
  }

  newPos(node: Node | null, pos: number): this {
    this.node = node;
    this.pos = pos;
    this.computeType();
    return this;
  }

  copy(): MutableDOMCursor {
    return new MutableDOMCursor(this.node, this.pos, this.filter);
  }

  mutable(): this { return this; }
  immutable(): DOMCursor { return new DOMCursor(this.node, this.pos, this.filter); }
  save(): DOMCursor { return this.immutable(); }

  restore(np: DOMCursor): this {
    this.node = np.node;
    this.pos = np.pos;
    this.filter = np.filter;
    return this;
  }

  emptyPrev(): this {
    this.type = 'empty';
    this.next = (up?: boolean) => {
      this.revertEmpty();
      return up ? (this as any).next(up) : this;
    };
    this.nodeAfter = (up?: boolean) => {
      this.computeType();
      return up ? (this as any).nodeAfter(up) : this;
    };
    this.prev = () => this;
    this.nodeBefore = () => this;
    return this;
  }

  emptyNext(): this {
    this.type = 'empty';
    this.prev = (up?: boolean) => {
      this.revertEmpty();
      return up ? (this as any).prev(up) : this;
    };
    this.nodeBefore = (up?: boolean) => {
      this.computeType();
      return up ? (this as any).nodeBefore(up) : this;
    };
    this.next = () => this;
    this.nodeAfter = () => this;
    return this;
  }

  private revertEmpty(): this {
    this.computeType();
    delete (this as any).next;
    delete (this as any).prev;
    delete (this as any).nodeAfter;
    delete (this as any).nodeBefore;
    return this;
  }
}

export { DOMCursor, EmptyDOMCursor, MutableDOMCursor };
export type { FilterResult, CursorType, FilterFunction };
