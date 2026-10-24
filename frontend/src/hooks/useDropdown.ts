import { useState, useRef, useEffect, type RefObject } from 'react';

// `portalRef`: a panel rendered elsewhere in the DOM (a portal) that still counts
// as inside, so tapping it doesn't close the dropdown.
export const useDropdown = (portalRef?: RefObject<HTMLElement | null>) => {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      const target = event.target as Node;
      if (portalRef?.current?.contains(target)) return;
      if (dropdownRef.current && !dropdownRef.current.contains(target)) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen, portalRef]);

  return { isOpen, dropdownRef, setIsOpen };
};
