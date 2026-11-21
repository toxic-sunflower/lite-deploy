(function () {
  // Tip is the small "?" next to a heading or a field: a click shows a short explanation, another click (or anywhere else) hides it.
  LU.components.Tip = {
    props: ['text'],
    data: function () { return { open: false }; },
    template: '<span class="tip"><button type="button" class="tipbtn" aria-label="What is this?" @click.stop.prevent="open = !open">?</button><span v-if="open" class="tippop" @click.stop>{{ text }}</span></span>',
    mounted: function () { var self = this; this._close = function () { self.open = false; }; document.addEventListener('click', this._close); },
    beforeUnmount: function () { document.removeEventListener('click', this._close); }
  };
})();
