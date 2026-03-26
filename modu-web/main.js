document.addEventListener('DOMContentLoaded', () => {
  // Intersection Observer for scroll animations
  const observerOptions = {
    root: null,
    rootMargin: '0px',
    threshold: 0.1
  };
  
  const observer = new IntersectionObserver((entries, observer) => {
    entries.forEach(entry => {
      if (entry.isIntersecting) {
        entry.target.classList.add('visible');
        observer.unobserve(entry.target);
      }
    });
  }, observerOptions);
  
  const elementsToAnimate = document.querySelectorAll('.fade-up');
  elementsToAnimate.forEach(el => observer.observe(el));
  
  // Copy to clipboard functionality
  const copyBtn = document.getElementById('copy-btn');
  const codeBlock = document.getElementById('code-block');
  
  if (copyBtn && codeBlock) {
    copyBtn.addEventListener('click', () => {
      const textToCopy = codeBlock.textContent;
      
      navigator.clipboard.writeText(textToCopy).then(() => {
        // Change icon to checkmark
        const originalHTML = copyBtn.innerHTML;
        copyBtn.innerHTML = `
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
        `;
        copyBtn.classList.add('copied');
        
        // Reset after 2 seconds
        setTimeout(() => {
          copyBtn.innerHTML = originalHTML;
          copyBtn.classList.remove('copied');
        }, 2000);
      }).catch(err => {
        console.error('Failed to copy text: ', err);
      });
    });
  }
});
